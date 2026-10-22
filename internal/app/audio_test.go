package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/audio"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/settings"
	"github.com/97Dmitry/solveo/internal/stt"
	sttdocker "github.com/97Dmitry/solveo/internal/stt/docker"
)

type fakeRuntime struct {
	mu       sync.Mutex
	checks   []sttdocker.Check
	gate     chan struct{} // when set, Prepare waits for it
	failWith error
	prepared int
	started  int
	stopped  int
}

func readyChecks() []sttdocker.Check {
	var out []sttdocker.Check
	for _, id := range []string{"docker", "engine", "image", "gpu", "model"} {
		out = append(out, sttdocker.Check{ID: id, State: sttdocker.StateOK, Message: id + " ok"})
	}
	return out
}

func (f *fakeRuntime) Status(context.Context) []sttdocker.Check {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.checks)
}

func (f *fakeRuntime) Prepare(ctx context.Context, progress func(string)) error {
	progress("Сборка образа")
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	f.prepared++
	f.checks = readyChecks()
	return nil
}

func (f *fakeRuntime) Start(context.Context, func(string)) (sttdocker.Endpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started++
	return sttdocker.Endpoint{Addr: "127.0.0.1:1", Token: "t"}, nil
}

func (f *fakeRuntime) Stop(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped++
	return nil
}

type openCall struct {
	stream, track string
	phrases       []string
}

type fakeSession struct {
	mu       sync.Mutex
	opens    []openCall
	pushes   map[string]int
	pushErr  error
	flushed  []string
	closedSt []string
	closed   bool
	events   chan any
}

func newFakeSession() *fakeSession {
	return &fakeSession{pushes: map[string]int{}, events: make(chan any, 64)}
}

func (s *fakeSession) Open(_ context.Context, streamID, track string, phrases []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opens = append(s.opens, openCall{streamID, track, slices.Clone(phrases)})
	return nil
}

func (s *fakeSession) Push(streamID string, _, _ int, _ []float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pushErr != nil {
		return s.pushErr
	}
	s.pushes[streamID]++
	return nil
}

func (s *fakeSession) Flush(_ context.Context, streamID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushed = append(s.flushed, streamID)
	return nil
}

func (s *fakeSession) CloseStream(streamID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closedSt = append(s.closedSt, streamID)
	return nil
}

func (s *fakeSession) Events() <-chan any { return s.events }

func (s *fakeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.events)
	}
	return nil
}

func (s *fakeSession) stream(track string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.opens {
		if o.track == track {
			return o.stream
		}
	}
	return ""
}

type fakeCapturer struct {
	mu       sync.Mutex
	sinks    map[audio.Source]func(audio.Frame)
	fails    map[audio.Source]func(error)
	startErr map[audio.Source]error
	stopped  map[audio.Source]int
}

func newFakeCapturer() *fakeCapturer {
	return &fakeCapturer{sinks: map[audio.Source]func(audio.Frame){}, fails: map[audio.Source]func(error){}, startErr: map[audio.Source]error{}, stopped: map[audio.Source]int{}}
}

func (f *fakeCapturer) Start(_ context.Context, src audio.Source, sink func(audio.Frame), fail func(error)) (audio.Device, func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.startErr[src]; err != nil {
		return audio.Device{}, nil, err
	}
	f.sinks[src], f.fails[src] = sink, fail
	return audio.Device{Name: string(src) + " device"}, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.stopped[src]++
	}, nil
}

func (f *fakeCapturer) frame(src audio.Source) {
	f.mu.Lock()
	sink := f.sinks[src]
	f.mu.Unlock()
	sink(audio.Frame{Rate: 48000, Channels: 2, Samples: make([]float32, 960)})
}

type audioHarness struct {
	c        *Controller
	provider *fakeProvider
	store    *history.Store
	runtime  *fakeRuntime
	session  *fakeSession
	capturer *fakeCapturer
}

func setupAudio(t *testing.T) *audioHarness {
	t.Helper()
	s, e := history.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	h := &audioHarness{
		provider: &fakeProvider{started: make(chan llm.Request, 5), finish: make(chan error, 5), store: s},
		store:    s,
		runtime:  &fakeRuntime{checks: readyChecks()},
		session:  newFakeSession(),
		capturer: newFakeCapturer(),
	}
	c, e := New(Dependencies{Store: s, SettingsStore: settings.Open(s.Dir), Provider: h.provider, Work: openWork(t, s), Audio: AudioDeps{
		Runtime:  h.runtime,
		Dial:     func(context.Context, string, string) (STTSession, error) { return h.session, nil },
		Capturer: h.capturer,
	}})
	if e != nil {
		t.Fatal(e)
	}
	h.c = c
	t.Cleanup(func() { _, _ = c.DiscardUnsaved(); _ = c.Close(context.Background()) })
	return h
}

func waitAudio(t *testing.T, c *Controller, what string, ok func(AudioView) bool) AudioView {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v := c.AudioSnapshot(); ok(v) {
			return v
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("audio never reached %s: %+v", what, c.AudioSnapshot())
	return AudioView{}
}

func (h *audioHarness) listen(t *testing.T) {
	t.Helper()
	if err := h.c.StartAudioModel(); err != nil {
		t.Fatal(err)
	}
	waitAudio(t, h.c, "ready", func(v AudioView) bool { return v.Model == "ready" && v.Busy == "" })
	if err := h.c.StartListening(); err != nil {
		t.Fatal(err)
	}
}

func (h *audioHarness) update(track, kind string, n, revision int, text string) {
	stream := h.session.stream(track)
	h.session.events <- stt.Update{StreamID: stream, UtteranceID: fmt.Sprintf("%s:%d", stream, n), Revision: revision, Kind: kind, Text: text}
}

func segments(v AudioView) string {
	var parts []string
	for _, s := range v.Segments {
		mark := "~"
		if s.Final {
			mark = "="
		}
		parts = append(parts, s.Track+mark+s.Text)
	}
	return strings.Join(parts, "|")
}

func TestAudioUnavailableWithoutRuntime(t *testing.T) {
	c, _, _ := setup(t)
	if c.AudioSnapshot().Available {
		t.Fatal("audio must be unavailable without a runtime")
	}
	if err := c.PrepareAudio(); err == nil {
		t.Fatal("prepare without a runtime must fail")
	}
}

func TestAudioOperationsAreExclusive(t *testing.T) {
	h := setupAudio(t)
	h.runtime.gate = make(chan struct{})
	if err := h.c.PrepareAudio(); err != nil {
		t.Fatal(err)
	}
	if err := h.c.PrepareAudio(); err == nil || !strings.Contains(err.Error(), "Подготовка уже идёт") {
		t.Fatalf("second prepare: %v", err)
	}
	if err := h.c.StartListening(); err == nil || !strings.Contains(err.Error(), "Сначала запустите модель") {
		t.Fatalf("listening before the model: %v", err)
	}
	close(h.runtime.gate)
	v := waitAudio(t, h.c, "prepared", func(v AudioView) bool { return v.Busy == "" })
	if v.Error != "" || len(v.Checks) != 5 {
		t.Fatalf("after prepare: %+v", v)
	}
}

func TestStartModelDialsAndListeningOpensBothTracks(t *testing.T) {
	h := setupAudio(t)
	cfg := h.c.Snapshot().Settings
	cfg.Audio.ContextPhrases = []string{"NestJS", "JWT"}
	if _, err := h.c.UpdateSettings(cfg); err != nil {
		t.Fatal(err)
	}
	h.listen(t)
	v := h.c.AudioSnapshot()
	if !v.Listening || len(v.Sources) != 2 || v.Sources[0].Device != "system device" || v.Sources[1].Track != "mic" {
		t.Fatalf("listening view %+v", v)
	}
	h.session.mu.Lock()
	opens := slices.Clone(h.session.opens)
	h.session.mu.Unlock()
	if len(opens) != 2 || opens[0].track != "system" || opens[1].track != "mic" || opens[0].stream == opens[1].stream {
		t.Fatalf("opens %+v", opens)
	}
	if !slices.Equal(opens[0].phrases, []string{"NestJS", "JWT"}) {
		t.Fatalf("dictionary not sent: %v", opens[0].phrases)
	}
	h.capturer.frame(audio.System)
	h.capturer.frame(audio.Mic)
	h.capturer.frame(audio.Mic)
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	if h.session.pushes[opens[0].stream] != 1 || h.session.pushes[opens[1].stream] != 2 {
		t.Fatalf("pushes %v", h.session.pushes)
	}
}

func TestListeningContinuesWhenOnlyMicFails(t *testing.T) {
	h := setupAudio(t)
	h.capturer.startErr[audio.Mic] = audio.ErrNoDevice
	h.listen(t)
	v := h.c.AudioSnapshot()
	if !v.Listening || v.Sources[0].Error != "" || v.Sources[1].Error == "" {
		t.Fatalf("view %+v", v)
	}
}

func TestTranscriptAppliesUpdatesByRevision(t *testing.T) {
	h := setupAudio(t)
	h.listen(t)
	h.update("system", stt.KindStarted, 1, 0, "")
	h.update("system", stt.KindPartial, 1, 2, "почему контекст")
	h.update("system", stt.KindPartial, 1, 1, "устаревший")
	h.update("mic", stt.KindStarted, 1, 0, "")
	h.update("mic", stt.KindFinal, 1, 1, "  ")
	h.update("system", stt.KindFinal, 1, 3, "Почему context cancellation?")
	v := waitAudio(t, h.c, "final", func(v AudioView) bool { return segments(v) == "system=Почему context cancellation?" })
	if v.Segments[0].StartedAt == "" || !strings.HasSuffix(v.Segments[0].StartedAt, "Z") {
		t.Fatalf("started_at must be RFC 3339 UTC: %q", v.Segments[0].StartedAt)
	}
	h.update("system", stt.KindPartial, 1, 4, "после финала")
	for i := 2; i <= maxTranscriptSegments+1; i++ {
		h.update("system", stt.KindFinal, i, 1, fmt.Sprint(i))
	}
	v = waitAudio(t, h.c, "eviction", func(v AudioView) bool {
		return len(v.Segments) == maxTranscriptSegments && v.Segments[len(v.Segments)-1].Text == fmt.Sprint(maxTranscriptSegments+1)
	})
	if v.Segments[0].Text != "2" {
		t.Fatalf("oldest segment not evicted: %q", v.Segments[0].Text)
	}
}

func TestStopListeningFlushesThenStopsCapture(t *testing.T) {
	h := setupAudio(t)
	h.listen(t)
	system, mic := h.session.stream("system"), h.session.stream("mic")
	if err := h.c.StopListening(); err != nil {
		t.Fatal(err)
	}
	if v := h.c.AudioSnapshot(); v.Listening {
		t.Fatal("still listening after stop")
	}
	h.capturer.mu.Lock()
	stopped := h.capturer.stopped[audio.System] == 1 && h.capturer.stopped[audio.Mic] == 1
	h.capturer.mu.Unlock()
	if !stopped {
		t.Fatal("capture not stopped")
	}
	h.update("system", stt.KindFinal, 1, 1, "хвост после остановки")
	waitAudio(t, h.c, "late final", func(v AudioView) bool { return segments(v) == "system=хвост после остановки" })
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.session.mu.Lock()
		flushed, closed := slices.Clone(h.session.flushed), slices.Clone(h.session.closedSt)
		h.session.mu.Unlock()
		if slices.Contains(flushed, system) && slices.Contains(flushed, mic) && len(closed) == 2 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("streams were not flushed and closed")
}

func TestWorkerLossStopsListening(t *testing.T) {
	h := setupAudio(t)
	h.listen(t)
	h.session.events <- errors.New("связь с распознавателем потеряна: EOF")
	_ = h.session.Close()
	v := waitAudio(t, h.c, "failed", func(v AudioView) bool { return v.Model == "failed" && !v.Listening })
	if !strings.Contains(v.Error, "связь с распознавателем потеряна") {
		t.Fatalf("error %q", v.Error)
	}
	h.capturer.mu.Lock()
	stopped := h.capturer.stopped[audio.System]
	h.capturer.mu.Unlock()
	if stopped != 1 {
		t.Fatal("capture not stopped after worker loss")
	}
	s := h.c.Snapshot()
	if _, err := h.c.Submit(SubmitInput{RequestID: "r", ChatID: s.ActiveChatID, Text: "обычный вопрос", DraftRevision: 1}); err != nil {
		t.Fatalf("chat must stay usable: %v", err)
	}
	<-h.provider.started
	h.provider.finish <- nil
	waitIdle(t, h.c)
}

func TestBackpressureMarksGap(t *testing.T) {
	h := setupAudio(t)
	h.listen(t)
	h.session.mu.Lock()
	h.session.pushErr = stt.ErrBackpressure
	h.session.mu.Unlock()
	h.capturer.frame(audio.System)
	waitAudio(t, h.c, "gap", func(v AudioView) bool { return strings.Contains(v.Error, "часть звука пропущена") })
	h.session.mu.Lock()
	h.session.pushErr = nil
	h.session.mu.Unlock()
	h.update("system", stt.KindFinal, 1, 1, "дальше")
	waitAudio(t, h.c, "recovered", func(v AudioView) bool { return v.Error == "" })
}

func (h *audioHarness) finals(t *testing.T, texts ...string) []string {
	t.Helper()
	var ids []string
	for i, text := range texts {
		h.update("system", stt.KindFinal, i+1, 1, text)
		ids = append(ids, fmt.Sprintf("%s:%d", h.session.stream("system"), i+1))
	}
	waitAudio(t, h.c, "finals", func(v AudioView) bool { return len(v.Segments) == len(texts) })
	return ids
}

func TestSubmitTranscriptPreservesDraftAndQueue(t *testing.T) {
	h := setupAudio(t)
	chatID := h.c.Snapshot().ActiveChatID
	if err := h.c.SetDraft(DraftInput{ChatID: chatID, Text: "мой черновик", Revision: 3}); err != nil {
		t.Fatal(err)
	}
	if err := h.c.ImportAttachment(chatID, "shot.png", base64.StdEncoding.EncodeToString(pngBytes(t))); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, h.c)
	h.listen(t)
	ids := h.finals(t, "Первая фраза.", "Вторая фраза?")
	if _, err := h.c.SubmitTranscript(SubmitTranscriptInput{ChatID: chatID, RequestID: "req", SegmentIDs: []string{ids[1], ids[0]}}); err != nil {
		t.Fatal(err)
	}
	req := <-h.provider.started
	h.provider.finish <- nil
	waitIdle(t, h.c)
	s := h.c.Snapshot()
	var turn string
	var attachments int
	for _, m := range s.Messages {
		if m.Role == "user" {
			turn, attachments = m.Text, len(m.AttachmentIDs)
		}
	}
	if turn != "Первая фраза.\nВторая фраза?" || attachments != 0 {
		t.Fatalf("turn %q with %d attachments", turn, attachments)
	}
	if len(req.Images) != 0 {
		t.Fatal("queued screenshots were sent with the transcript")
	}
	if s.Draft.Text != "мой черновик" || s.Draft.Revision != 3 || len(s.Queue) != 1 {
		t.Fatalf("draft %+v queue %d", s.Draft, len(s.Queue))
	}
}

func TestSubmitTranscriptRejections(t *testing.T) {
	h := setupAudio(t)
	chatID := h.c.Snapshot().ActiveChatID
	h.listen(t)
	ids := h.finals(t, "готово")
	h.update("system", stt.KindStarted, 9, 0, "")
	h.update("system", stt.KindPartial, 9, 1, "ещё говорят")
	waitAudio(t, h.c, "partial", func(v AudioView) bool { return len(v.Segments) == 2 })
	partial := fmt.Sprintf("%s:9", h.session.stream("system"))
	cases := []struct {
		in   SubmitTranscriptInput
		want string
	}{
		{SubmitTranscriptInput{ChatID: chatID, RequestID: "a", SegmentIDs: []string{partial}}, "Фраза ещё распознаётся"},
		{SubmitTranscriptInput{ChatID: chatID, RequestID: "b", SegmentIDs: []string{"нет"}}, "Фраза больше недоступна"},
		{SubmitTranscriptInput{ChatID: chatID, RequestID: "c"}, ErrEmpty.Error()},
		{SubmitTranscriptInput{ChatID: "stale", RequestID: "d", SegmentIDs: ids}, ErrStale.Error()},
	}
	for _, tc := range cases {
		if _, err := h.c.SubmitTranscript(tc.in); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: got %v, want %q", tc.in, err, tc.want)
		}
	}
	if _, err := h.c.Submit(SubmitInput{RequestID: "busy", ChatID: chatID, Text: "занят", DraftRevision: 1}); err != nil {
		t.Fatal(err)
	}
	<-h.provider.started
	if _, err := h.c.SubmitTranscript(SubmitTranscriptInput{ChatID: chatID, RequestID: "e", SegmentIDs: ids}); !errors.Is(err, ErrBusy) {
		t.Fatalf("while busy: %v", err)
	}
	h.provider.finish <- nil
	waitIdle(t, h.c)
}

func TestCloseStopsListeningAndContainer(t *testing.T) {
	h := setupAudio(t)
	h.listen(t)
	if err := h.c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.capturer.mu.Lock()
	captureStopped := h.capturer.stopped[audio.System] == 1 && h.capturer.stopped[audio.Mic] == 1
	h.capturer.mu.Unlock()
	h.session.mu.Lock()
	sessionClosed := h.session.closed
	h.session.mu.Unlock()
	h.runtime.mu.Lock()
	stopped := h.runtime.stopped
	h.runtime.mu.Unlock()
	if !captureStopped || !sessionClosed || stopped != 1 {
		t.Fatalf("capture %v session %v container stops %d", captureStopped, sessionClosed, stopped)
	}
}

func TestLongErrorsAreShortenedForTheUI(t *testing.T) {
	h := setupAudio(t)
	long := "сбой: " + strings.Repeat("traceback line\n", 2000)
	h.runtime.failWith = errors.New(long)
	h.runtime.checks = []sttdocker.Check{{ID: "gpu", State: sttdocker.StateError, Message: long}}
	if err := h.c.PrepareAudio(); err != nil {
		t.Fatal(err)
	}
	v := waitAudio(t, h.c, "failed prepare", func(v AudioView) bool { return v.Busy == "" && v.Error != "" })
	if n := len([]rune(v.Error)); n > maxUITextRunes+1 || !strings.HasPrefix(v.Error, "сбой: ") {
		t.Fatalf("error of %d runes", n)
	}
	if n := len([]rune(v.Checks[0].Message)); n > maxUITextRunes+1 {
		t.Fatalf("check message of %d runes", n)
	}
}
