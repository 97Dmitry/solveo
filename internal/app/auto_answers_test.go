package app

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/audio"
	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/hotkeys"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/questionbuilder"
	"github.com/97Dmitry/solveo/internal/settings"
	"github.com/97Dmitry/solveo/internal/stt"
)

type failOneAutoSave struct {
	Archive
	fail atomic.Bool
}

func (s *failOneAutoSave) Save(in history.SaveInput) (chat.Chat, error) {
	if s.fail.Swap(false) {
		return chat.Chat{}, errors.New("test archive write failure")
	}
	return s.Archive.Save(in)
}

func awaitAutoAnalysis(t *testing.T, p *questionFake) questionbuilder.Input {
	t.Helper()
	select {
	case in := <-p.inputs:
		return in
	case <-time.After(5 * time.Second):
		t.Fatal("automatic analysis did not start")
		return questionbuilder.Input{}
	}
}

func testDedicatedAutoAnswers(t *testing.T, audioDeps ...AudioDeps) (*Controller, *questionFake, Dependencies) {
	t.Helper()
	store, err := history.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	autoStore, err := history.Open(filepath.Join(store.Dir, "auto-answers"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := settings.Open(store.Dir)
	if err := cfg.Save(settings.Config{Theme: "dark", FontSize: 17, UIScale: 100, Hotkeys: hotkeys.Defaults(), Audio: settings.Audio{BuilderModel: "fake"}}); err != nil {
		t.Fatal(err)
	}
	f := &fakeProvider{started: make(chan llm.Request, 8), finish: make(chan error, 8)}
	p := &questionFake{fakeProvider: f, inputs: make(chan questionbuilder.Input, 8), results: make(chan questionbuilder.Result, 8)}
	deps := Dependencies{Store: store, SettingsStore: cfg, Work: openWork(t, store), AutoStore: autoStore, AutoWork: openWork(t, autoStore), Provider: p}
	if len(audioDeps) != 0 {
		deps.Audio = audioDeps[0]
	}
	c, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c, p, deps
}

type oneSystemFailure struct {
	*fakeSession
	fail atomic.Bool
}

type delayedSpeechStart struct {
	*fakeRuntime
	entered chan struct{}
	release chan struct{}
}

type failsDuringAutoOpen struct{ *fakeCapturer }

func (f failsDuringAutoOpen) Start(ctx context.Context, source audio.Source, sink func(audio.Frame), fail func(error)) (audio.Device, func(), error) {
	device, stop, err := f.fakeCapturer.Start(ctx, source, sink, fail)
	if err == nil && source == audio.System {
		fail(errors.New("system source lost during startup"))
	}
	return device, stop, err
}

func (s *delayedSpeechStart) Start(ctx context.Context, progress func(string)) (stt.SessionAPI, error) {
	close(s.entered)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.release:
		return s.fakeRuntime.Start(ctx, progress)
	}
}

func (s *oneSystemFailure) Open(ctx context.Context, streamID, track string, phrases []string) error {
	if track == "system" && s.fail.Swap(false) {
		return context.DeadlineExceeded
	}
	return s.fakeSession.Open(ctx, streamID, track, phrases)
}

func TestStartAutoAnswersRequiresSystemAndRetriesWithoutInterruptingMic(t *testing.T) {
	session := &oneSystemFailure{fakeSession: newFakeSession()}
	session.fail.Store(true)
	engine := &fakeRuntime{checks: readyChecks(), session: session}
	capture := newFakeCapturer()
	c, _, _ := testDedicatedAutoAnswers(t, AudioDeps{Providers: []stt.Provider{engine}, Capturer: capture})
	id := c.AutoAnswersSnapshot().SessionID
	if err := c.AutoAnswersCommand("startAnswers", id, "", ""); err != nil {
		t.Fatal(err)
	}
	waitAutoStart(t, c, func(v AutoAnswersView) bool { return v.State == "error" })
	if view := c.AutoAnswersSnapshot(); view.Running {
		t.Fatal("auto answers enabled without system capture", view)
	}
	mic := session.stream("mic")
	if mic == "" || !c.AudioSnapshot().Listening {
		t.Fatal("mic fallback did not stay active")
	}
	if err := c.AutoAnswersCommand("startAnswers", id, "", ""); err != nil {
		t.Fatal(err)
	}
	waitAutoStart(t, c, func(v AutoAnswersView) bool { return v.State == "listening" })
	if session.stream("system") == "" || session.stream("mic") != mic {
		t.Fatal("system retry changed the active mic")
	}
	capture.mu.Lock()
	stopped := capture.stopped[audio.Mic]
	capture.mu.Unlock()
	if stopped != 0 {
		t.Fatal("retry stopped the mic", stopped)
	}
	system := session.stream("system")
	c.audioCaptureFailed(system)(errors.New("test input loss"))
	waitAutoStart(t, c, func(v AutoAnswersView) bool { return v.State == "error" && !v.Running })
	if err := c.AutoAnswersCommand("startAnswers", id, "", ""); err != nil {
		t.Fatal(err)
	}
	waitAutoStart(t, c, func(v AutoAnswersView) bool { return v.State == "listening" })
	session.mu.Lock()
	retired := slices.Contains(session.closedSt, system)
	micRetired := slices.Contains(session.closedSt, mic)
	session.mu.Unlock()
	if !retired || micRetired {
		t.Fatal("retry did not retire just the failed system stream", retired, micRetired)
	}
}

func TestPauseWhileAutomaticAnswersStartDoesNotArmCapture(t *testing.T) {
	session := newFakeSession()
	engine := &delayedSpeechStart{
		fakeRuntime: &fakeRuntime{checks: readyChecks(), session: session},
		entered:     make(chan struct{}),
		release:     make(chan struct{}, 1),
	}
	c, _, _ := testDedicatedAutoAnswers(t, AudioDeps{Providers: []stt.Provider{engine}, Capturer: newFakeCapturer()})
	id := c.AutoAnswersSnapshot().SessionID
	if err := c.AutoAnswersCommand("startAnswers", id, "", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-engine.entered:
	case <-time.After(time.Second):
		t.Fatal("speech model did not start")
	}
	if view := c.AutoAnswersSnapshot(); view.State != "starting_model" || view.Running {
		t.Fatal(view)
	}
	if err := c.AutoAnswersCommand("pauseAnswers", id, "", ""); err != nil {
		t.Fatal(err)
	}
	engine.release <- struct{}{}
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		view := c.AudioSnapshot()
		if view.Model == audioModelReady && view.Busy == "" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if view := c.AutoAnswersSnapshot(); view.State != "paused" || view.Running || c.AudioSnapshot().Listening {
		t.Fatal("startup survived pause", view, c.AudioSnapshot().Listening)
	}
}

func TestStartAutoAnswersDoesNotAdvertiseSystemSourceLostDuringOpening(t *testing.T) {
	engine := &fakeRuntime{checks: readyChecks(), session: newFakeSession()}
	capture := failsDuringAutoOpen{newFakeCapturer()}
	c, _, _ := testDedicatedAutoAnswers(t, AudioDeps{Providers: []stt.Provider{engine}, Capturer: capture})
	id := c.AutoAnswersSnapshot().SessionID
	if err := c.AutoAnswersCommand("startAnswers", id, "", ""); err != nil {
		t.Fatal(err)
	}
	waitAutoStart(t, c, func(v AutoAnswersView) bool { return v.State == "error" })
	if v := c.AutoAnswersSnapshot(); v.Running || !strings.Contains(v.Error, "system source lost during startup") {
		t.Fatal(v)
	}
	for _, source := range c.AudioSnapshot().Sources {
		if source.Track == "system" && source.Active {
			t.Fatal("a failed source was published as active", source)
		}
	}
}

func TestAutomaticStartExplainsMissingLocalRecognizerPreparation(t *testing.T) {
	engine := &fakeRuntime{
		checks:  []stt.Check{{ID: "model", State: stt.StateMissing}},
		session: newFakeSession(),
	}
	c, _, _ := testDedicatedAutoAnswers(t, AudioDeps{Providers: []stt.Provider{engine}, Capturer: newFakeCapturer()})
	id := c.AutoAnswersSnapshot().SessionID
	if err := c.AutoAnswersCommand("startAnswers", id, "", ""); err != nil {
		t.Fatal(err)
	}
	waitAutoStart(t, c, func(v AutoAnswersView) bool { return v.State == "setup" && !v.Busy })
	if v := c.AutoAnswersSnapshot(); v.SetupHint == "" || v.Running {
		t.Fatal(v)
	}
	engine.mu.Lock()
	started := engine.started
	engine.mu.Unlock()
	if started != 0 {
		t.Fatal("controller tried to start a missing local model")
	}
}

func armTestAutoAnswers(t *testing.T, c *Controller, sessionID string) {
	t.Helper()
	c.mu.Lock()
	cfg, preset := c.settings.Audio, chat.Clone(c.active)
	c.mu.Unlock()
	if err := c.resumeAutoAnswers(sessionID, cfg.BuilderModel, cfg.BuilderReasoning, preset); err != nil {
		t.Fatal(err)
	}
}

func waitAutoStart(t *testing.T, c *Controller, condition func(AutoAnswersView) bool) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); {
		if condition(c.AutoAnswersSnapshot()) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("automatic startup did not settle", c.AutoAnswersSnapshot())
}

func TestDedicatedAutoAnswersContinueAlongsideManualChatAndRestore(t *testing.T) {
	c, provider, deps := testDedicatedAutoAnswers(t)
	main := c.Snapshot()
	auto := c.AutoAnswersSnapshot()
	if auto.SessionID == main.ActiveChatID || !auto.Available {
		t.Fatal(auto)
	}
	armTestAutoAnswers(t, c, auto.SessionID)
	manual, err := c.Submit(SubmitInput{ChatID: main.ActiveChatID, RequestID: chat.ID(), Text: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	_ = nextAutomaticRequest(t, provider.fakeProvider)
	c.observeQuestion(main.ActiveChatID, questionbuilder.Segment{ID: "types", Track: "system", Final: true, At: time.Now().UTC().Format(time.RFC3339Nano), Text: "Какие есть типы данных?"})
	in := awaitAutoAnalysis(t, provider)
	if in.ChatID != auto.SessionID || len(in.Segments) != 1 {
		t.Fatal(in)
	}
	provider.results <- questionbuilder.Result{HasQuestion: true, Ready: true, Text: "Какие есть типы данных?", SourceIDs: []string{"types"}}
	req := nextAutomaticRequest(t, provider.fakeProvider)
	if req.ChatID != auto.SessionID || len(req.History) != 0 || c.Snapshot().Operation.ID != manual {
		t.Fatal("automatic answer reused or displaced the main operation", req)
	}
	if err := c.Cancel(manual); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, c)
	provider.finish <- nil
	waitIdle(t, c.autoAnswers)
	detail, err := c.AutoAnswerDetail(auto.SessionID, req.Turn.ID)
	if err != nil || detail.Text != "answer" || len(c.Snapshot().Messages) != 1 {
		t.Fatal(detail, err)
	}
	if _, err := c.NewChat(); err != nil {
		t.Fatal(err)
	}
	if !c.AutoAnswersSnapshot().Running {
		t.Fatal("main chat navigation paused automatic answers")
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close(context.Background()) })
	if view := restored.AutoAnswersSnapshot(); view.Running || view.SessionID != auto.SessionID || len(view.Items) != 1 || view.Items[0].ID != req.Turn.ID {
		t.Fatal("saved auto session was not restored independently", view)
	}
	armTestAutoAnswers(t, restored, auto.SessionID)
	restored.observeQuestion(restored.Snapshot().ActiveChatID, questionbuilder.Segment{ID: "numbers", Track: "system", Final: true, At: time.Now().UTC().Format(time.RFC3339Nano), Text: "А точнее, числовые. И как работает GC?"})
	next := awaitAutoAnalysis(t, provider)
	if len(next.Recent) != 1 || next.Recent[0].ID != req.Turn.ID {
		t.Fatal("reopened session lost prior question context", next)
	}
	provider.results <- questionbuilder.Result{
		HasQuestion: true, Ready: true, FollowUp: true, Refinement: true, TargetQuestionID: req.Turn.ID,
		Text: "Какие есть числовые типы данных?", SourceIDs: []string{"numbers"},
		Additional: []questionbuilder.AdditionalQuestion{{Text: "Как работает GC?", SourceIDs: []string{"numbers"}}},
	}
	correction := nextAutomaticRequest(t, provider.fakeProvider)
	if correction.Turn.ID != req.Turn.ID || !correction.Turn.Question.Refinement {
		t.Fatal("restored session failed to revise the same card", correction)
	}
	sources := correction.Turn.Question.Sources
	if len(sources) != 2 || sources[0].Seq >= sources[1].Seq {
		t.Fatal("restored correction lost chronological order", sources)
	}
	provider.finish <- nil
	separate := nextAutomaticRequest(t, provider.fakeProvider)
	if separate.Turn.ID == req.Turn.ID || len(separate.History) != 2 || separate.Turn.Text != "Как работает GC?" {
		t.Fatal("mixed utterance did not yield a second independent question", separate)
	}
	provider.finish <- nil
	waitIdle(t, restored.autoAnswers)
	if view := restored.AutoAnswersSnapshot(); len(view.Items) != 2 || view.Items[0].ID != req.Turn.ID || !view.Items[0].Revised {
		t.Fatal(view)
	}
}

func TestAutoAnswersRejectStaleRecoveryAndEmptySessionTarget(t *testing.T) {
	c, _, _ := testDedicatedAutoAnswers(t)
	id := c.AutoAnswersSnapshot().SessionID
	if err := c.AutoAnswersCommand("openAnswers", "", "", ""); !errors.Is(err, ErrStale) {
		t.Fatal("empty open target created a session", err)
	}
	a := c.autoAnswers
	a.mu.Lock()
	cardID := chat.ID()
	attemptID := chat.ID()
	a.active.Messages = append(a.active.Messages, chat.Message{ID: cardID, Role: chat.RoleUser, Attempts: []chat.Attempt{{ID: attemptID}}})
	a.pending = &pendingSave{doc: chat.Clone(a.active)}
	a.op = operation{Operation: Operation{Phase: phaseSavePending, Kind: kindSubmit}}
	a.mu.Unlock()
	t.Cleanup(func() {
		a.mu.Lock()
		a.pending = nil
		a.op = operation{Operation: Operation{Phase: phaseIdle}}
		a.mu.Unlock()
	})
	for _, action := range []string{"retryAnswer", "discardAnswer"} {
		if err := c.AutoAnswersCommand(action, id, chat.ID(), attemptID); !errors.Is(err, ErrStale) {
			t.Fatal(action, err)
		}
		if err := c.AutoAnswersCommand(action, id, cardID, chat.ID()); !errors.Is(err, ErrStale) {
			t.Fatal(action, err)
		}
	}
	if detail, err := c.AutoAnswerDetail(id, cardID); err != nil || detail.Status != "pending" {
		t.Fatal("the actual unsaved card was affected", detail, err)
	}
}

func TestFailedAutoRevisionKeepsCompletedAnswerAcrossRestart(t *testing.T) {
	c, provider, deps := testDedicatedAutoAnswers(t)
	mainID := c.Snapshot().ActiveChatID
	session := c.AutoAnswersSnapshot().SessionID
	armTestAutoAnswers(t, c, session)
	say := func(id, text, target string) {
		c.observeQuestion(mainID, questionbuilder.Segment{ID: id, Track: "system", Final: true, At: time.Now().UTC().Format(time.RFC3339Nano), Text: text})
		_ = awaitAutoAnalysis(t, provider)
		provider.results <- questionbuilder.Result{
			HasQuestion: true, Ready: true, FollowUp: target != "", Refinement: target != "", TargetQuestionID: target,
			Text: text, SourceIDs: []string{id},
		}
	}
	say("types", "Какие есть типы данных?", "")
	first := nextAutomaticRequest(t, provider.fakeProvider)
	provider.finish <- nil
	waitIdle(t, c.autoAnswers)
	say("numbers", "А точнее, числовые", first.Turn.ID)
	_ = nextAutomaticRequest(t, provider.fakeProvider)
	provider.finish <- errors.New("replacement failed")
	waitIdle(t, c.autoAnswers)
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close(context.Background()) })
	detail, err := restored.AutoAnswerDetail(session, first.Turn.ID)
	if err != nil || detail.Text != "answer" || detail.Question != "Какие есть типы данных?" || !detail.SavedAnswer || detail.Status != chat.StatusFailed {
		t.Fatal("failed auto revision lost the last saved answer", detail, err)
	}
	if err := restored.AutoAnswersCommand("retryAnswer", session, first.Turn.ID, detail.Revision); err != nil {
		t.Fatal(err)
	}
	retry := nextAutomaticRequest(t, provider.fakeProvider)
	if retry.Turn.ID != first.Turn.ID || retry.Revision == nil || retry.Revision.Answer != "answer" {
		t.Fatal("manual retry lost the revision target", retry)
	}
	provider.finish <- nil
	waitIdle(t, restored.autoAnswers)
	detail, err = restored.AutoAnswerDetail(session, first.Turn.ID)
	if err != nil || detail.Question != "А точнее, числовые" || !detail.SavedAnswer || detail.Status != chat.StatusComplete {
		t.Fatal("explicit retry did not replace the saved answer", detail, err)
	}
}

func TestAutoAnswersReviseEarlierCardWithoutInterruptingLaterQuestion(t *testing.T) {
	c, provider, _ := testDedicatedAutoAnswers(t)
	archive := &failOneAutoSave{Archive: c.autoAnswers.deps.Store}
	c.autoAnswers.deps.Store = archive
	main := c.Snapshot().ActiveChatID
	session := c.AutoAnswersSnapshot().SessionID
	armTestAutoAnswers(t, c, session)
	observe := func(id, text string) {
		c.observeQuestion(main, questionbuilder.Segment{
			ID: id, Track: "system", Final: true, At: time.Now().UTC().Format(time.RFC3339Nano), Text: text,
		})
	}
	observe("first", "Какие есть типы данных?")
	_ = awaitAutoAnalysis(t, provider)
	provider.results <- questionbuilder.Result{HasQuestion: true, Ready: true, Text: "Какие есть типы данных?", SourceIDs: []string{"first"}}
	first := nextAutomaticRequest(t, provider.fakeProvider)
	provider.finish <- nil
	waitIdle(t, c.autoAnswers)
	before, err := c.AutoAnswerDetail(session, first.Turn.ID)
	if err != nil {
		t.Fatal(err)
	}

	observe("noise", "Э-э… Угу")
	observe("second", "Как работает GC?")
	in := awaitAutoAnalysis(t, provider)
	if len(in.Segments) != 1 || in.Segments[0].ID != "second" {
		t.Fatal("filler entered analysis", in.Segments)
	}
	provider.results <- questionbuilder.Result{HasQuestion: true, Ready: true, Text: "Как работает GC?", SourceIDs: []string{"second"}}
	second := nextAutomaticRequest(t, provider.fakeProvider)
	if after, err := c.AutoAnswerDetail(session, first.Turn.ID); err != nil || after != before {
		t.Fatal("noise or a separate question changed the completed answer", before, after, err)
	}
	observe("third", "Вернёмся к типам данных, мне нужны числовые")
	in = awaitAutoAnalysis(t, provider)
	if len(in.Recent) != 2 || in.Recent[0].ID != first.Turn.ID || in.RecentAnswers[first.Turn.ID] != "answer" {
		t.Fatal("earlier question and answer are missing from the analyzer input", in.Recent, in.RecentAnswers)
	}
	provider.results <- questionbuilder.Result{
		HasQuestion: true, Ready: true, FollowUp: true, Refinement: true, TargetQuestionID: first.Turn.ID,
		Text: "Какие есть числовые типы данных?", SourceIDs: []string{"third"},
	}
	var pinned, interrupted bool
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		c.autoAnswers.mu.Lock()
		pinned = c.autoAnswers.question.autoRefinement != nil
		interrupted = c.autoAnswers.op.superseded
		c.autoAnswers.mu.Unlock()
		if pinned {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !pinned || interrupted {
		t.Fatal("the earlier refinement did not wait independently", pinned, interrupted)
	}
	provider.finish <- nil
	revision := nextAutomaticRequest(t, provider.fakeProvider)
	if revision.Turn.ID != first.Turn.ID || revision.Revision == nil || revision.Revision.Answer != "answer" {
		t.Fatal("the provider did not get the correct earlier target", revision)
	}
	if len(revision.History) != 2 || revision.History[0].ID != second.Turn.ID {
		t.Fatal("other questions were not preserved separately", revision.History)
	}
	during, err := c.AutoAnswerDetail(session, first.Turn.ID)
	if err != nil || during.Status != "refining" || !during.SavedAnswer || during.Text != before.Text {
		t.Fatal("the saved answer disappeared during generation", during, err)
	}
	archived, err := c.autoAnswers.deps.Store.Load(session)
	if err != nil || archived.AnswerRevisions[first.Turn.ID].Attempt.Status != chat.StatusInterrupted ||
		archived.Messages[0].Text != before.Question || archived.Messages[1].Text != before.Text {
		t.Fatal("restart during a staged revision would lose the saved version or miss the interruption", archived, err)
	}
	observe("fourth", "И отдельно числа с плавающей точкой")
	in = awaitAutoAnalysis(t, provider)
	if in.Recent[0].Text != revision.Turn.Text {
		t.Fatal("the analyzer did not see the open revision", in.Recent)
	}
	provider.results <- questionbuilder.Result{
		HasQuestion: true, Ready: true, FollowUp: true, Refinement: true, TargetQuestionID: first.Turn.ID,
		Text: "Какие есть числовые типы и чем отличаются числа с плавающей точкой?", SourceIDs: []string{"fourth"},
	}
	lastRevision := nextAutomaticRequest(t, provider.fakeProvider)
	sources := lastRevision.Turn.Question.Sources
	if len(sources) != 3 || sources[0].ID != "first" || sources[1].ID != "third" || sources[2].ID != "fourth" {
		t.Fatal("superseding a revision lost accepted speech", sources)
	}
	archive.fail.Store(true)
	provider.finish <- nil
	waitIdle(t, c.autoAnswers)
	pending, err := c.AutoAnswerDetail(session, first.Turn.ID)
	if err != nil || pending.Status != "pending" || !pending.SavedAnswer || pending.Text != before.Text {
		t.Fatal("failed archive write exposed an uncommitted revision", pending, err)
	}
	if err := c.AutoAnswersCommand("retryAnswer", session, first.Turn.ID, pending.Revision); err != nil {
		t.Fatal(err)
	}
	select {
	case extra := <-provider.started:
		t.Fatal("retrying the archive write unexpectedly called Codex", extra)
	default:
	}
	view := c.AutoAnswersSnapshot()
	if len(view.Items) != 2 || view.Items[0].ID != first.Turn.ID || view.Items[0].Question != lastRevision.Turn.Text || view.Items[1].ID != second.Turn.ID ||
		view.Items[0].Status != chat.StatusComplete || view.Items[1].Status != chat.StatusComplete {
		t.Fatal("the wrong cards were published", view.Items)
	}
}

func TestAutoAnswersPreserveSpeechOrderWhenBatchEndsInEarlierRevision(t *testing.T) {
	c, provider, _ := testDedicatedAutoAnswers(t)
	main, session := c.Snapshot().ActiveChatID, c.AutoAnswersSnapshot().SessionID
	armTestAutoAnswers(t, c, session)
	observe := func(id, text string) {
		c.observeQuestion(main, questionbuilder.Segment{ID: id, Track: "system", Text: text, Final: true, At: time.Now().UTC().Format(time.RFC3339Nano)})
		_ = awaitAutoAnalysis(t, provider)
	}
	observe("types", "Какие есть типы данных?")
	provider.results <- questionbuilder.Result{HasQuestion: true, Ready: true, Text: "Какие есть типы данных?", SourceIDs: []string{"types"}}
	first := nextAutomaticRequest(t, provider.fakeProvider)
	provider.finish <- nil
	waitIdle(t, c.autoAnswers)
	observe("mixed", "Как работает GC? И вернёмся к типам данных, только числовые")
	provider.results <- questionbuilder.Result{
		HasQuestion: true, Ready: true, Text: "Как работает GC?", SourceIDs: []string{"mixed"},
		Additional: []questionbuilder.AdditionalQuestion{{
			Text: "Какие есть числовые типы данных?", SourceIDs: []string{"mixed"},
			Refinement: true, TargetQuestionID: first.Turn.ID,
		}},
	}
	second := nextAutomaticRequest(t, provider.fakeProvider)
	if second.Turn.ID == first.Turn.ID || second.Turn.Text != "Как работает GC?" {
		t.Fatal("the first question in the batch was not answered first", second.Turn)
	}
	provider.finish <- nil
	refinement := nextAutomaticRequest(t, provider.fakeProvider)
	if refinement.Turn.ID != first.Turn.ID || refinement.Revision == nil || len(refinement.AutoAnswers) != 1 || refinement.AutoAnswers[0].MessageID != second.Turn.ID {
		t.Fatal("the queued refinement targeted the wrong card or lost the new answer", refinement)
	}
	provider.finish <- nil
	waitIdle(t, c.autoAnswers)
	if cards := c.AutoAnswersSnapshot().Items; len(cards) != 2 || cards[0].ID != first.Turn.ID || !cards[0].Revised || cards[1].ID != second.Turn.ID {
		t.Fatal(cards)
	}
}
