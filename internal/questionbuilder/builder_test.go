package questionbuilder

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func seg(id, track, text string) Segment {
	return Segment{ID: id, Track: track, Text: text, At: time.Now().UTC().Format(time.RFC3339Nano), Final: true}
}
func ready(t *testing.T, b *Builder) Input {
	t.Helper()
	in, ok := b.Begin(time.Now())
	if !ok {
		t.Fatal("no input")
	}
	if err := b.Apply(in, Result{HasQuestion: true, Ready: true, Text: "Explain channels", SourceIDs: []string{"s"}}, nil); err != nil {
		t.Fatal(err)
	}
	return in
}
func TestFreshnessAndManualVersion(t *testing.T) {
	b := New("c")
	b.Enable(true)
	b.Observe(seg("m", "mic", "my answer"))
	if _, ok := b.Begin(time.Now()); ok {
		t.Fatal("mic triggered analysis")
	}
	b.Observe(seg("s", "system", "Explain channels"))
	ready(t, b)
	if !b.View().CanSend {
		t.Fatal(b.View())
	}
	b.Observe(Segment{ID: "p", Track: "system", At: time.Now().UTC().Format(time.RFC3339Nano)})
	if b.View().CanSend {
		t.Fatal("partial did not block")
	}
	if err := b.Edit("custom"); err != nil {
		t.Fatal(err)
	}
	if b.View().CanSend {
		t.Fatal("unsaved edit")
	}
	if err := b.SaveEdit(); err != nil {
		t.Fatal(err)
	}
	if !b.View().CanSend {
		t.Fatal("saved edit blocked by partial")
	}
	b.Observe(seg("p", "system", "and closing"))
	if _, ok := b.Begin(time.Now()); ok {
		t.Fatal("manual version overwritten")
	}
}
func TestSkipInvalidatesSnapshotRetainsTail(t *testing.T) {
	b := New("c")
	b.Enable(true)
	b.Observe(seg("s", "system", "first"))
	in, _ := b.Begin(time.Now())
	b.Observe(seg("tail", "system", "second"))
	b.Skip()
	_ = b.Apply(in, Result{HasQuestion: true, Ready: true, Text: "old", SourceIDs: []string{"s"}}, nil)
	if b.View().Text != "" {
		t.Fatal("resurrected")
	}
	next, ok := b.Begin(time.Now())
	if !ok || len(next.Segments) != 1 || next.Segments[0].ID != "tail" {
		t.Fatal(next)
	}
}
func TestLateResultCannotOverwriteEdit(t *testing.T) {
	b := New("c")
	b.Enable(true)
	b.Observe(seg("s", "system", "question"))
	in, _ := b.Begin(time.Now())
	if err := b.Edit("mine"); err != nil {
		t.Fatal(err)
	}
	_ = b.Apply(in, Result{HasQuestion: true, Ready: true, Text: "late", SourceIDs: []string{"s"}}, nil)
	if b.View().Text != "mine" {
		t.Fatal(b.View())
	}
}
func TestResultValidationAndBounds(t *testing.T) {
	b := New("c")
	b.Enable(true)
	b.Observe(seg("s", "system", "question"))
	in, _ := b.Begin(time.Now())
	if b.Apply(in, Result{HasQuestion: true, Ready: true, Text: "invented", SourceIDs: []string{"foreign"}}, nil) == nil {
		t.Fatal("foreign ID accepted")
	}
	if b.View().CanSend || b.View().Error == "" {
		t.Fatal(b.View())
	}
	b = New("c")
	b.Enable(true)
	for i := 0; i < 81; i++ {
		s := seg(string(rune('a'+i)), "system", "question")
		b.Observe(s)
	}
	if b.View().Error == "" {
		t.Fatal("overflow silently dropped")
	}
	if _, ok := b.Begin(time.Now()); ok {
		t.Fatal("overflow analyzed")
	}
}
func TestNewFinalDoesNotInvalidateSnapshot(t *testing.T) {
	b := New("c")
	b.Enable(true)
	b.Observe(seg("s", "system", "question"))
	in, _ := b.Begin(time.Now())
	b.Observe(seg("tail", "system", "extra"))
	if err := b.Apply(in, Result{HasQuestion: true, Ready: true, Text: "first", SourceIDs: []string{"s"}}, nil); err != nil {
		t.Fatal(err)
	}
	if b.View().Text != "first" || b.View().CanSend {
		t.Fatal(b.View())
	}
	next, ok := b.Begin(time.Now())
	if !ok || next.Current.Text != "first" {
		t.Fatal(next)
	}
}

func TestOverflowStopsIngestionAndSkipRecovers(t *testing.T) {
	b := New("c")
	b.Enable(true)
	for i := 0; i < 10000; i++ {
		b.Observe(seg(string(rune(100+i)), "system", "question"))
	}
	if len(b.window) > 81 || b.View().Enabled || b.View().Error == "" {
		t.Fatal(len(b.window), b.View())
	}
	b.Skip()
	b.Enable(true)
	b.Observe(seg("fresh", "system", "fresh question"))
	in, ok := b.Begin(time.Now())
	if !ok || len(in.Segments) != 1 {
		t.Fatal(in, b.View())
	}
}
func TestManualDuringFirstAnalysisPreservesLaterFinals(t *testing.T) {
	b := New("c")
	b.Enable(true)
	b.Observe(seg("s", "system", "first"))
	_, _ = b.Begin(time.Now())
	_ = b.Edit("my version")
	_ = b.SaveEdit()
	b.Observe(seg("later", "system", "next question"))
	b.Sent("sent")
	in, ok := b.Begin(time.Now())
	if !ok || len(in.Segments) != 1 || in.Segments[0].ID != "later" {
		t.Fatal(in, b.View())
	}
}
func TestSkipFailedSnapshotPreservesTail(t *testing.T) {
	b := New("c")
	b.Enable(true)
	b.Observe(seg("s", "system", "first"))
	in, _ := b.Begin(time.Now())
	b.Observe(seg("later", "system", "tail"))
	_ = b.Apply(in, Result{HasQuestion: true, Ready: true, Text: "bad", SourceIDs: []string{"foreign"}}, nil)
	b.Skip()
	in, ok := b.Begin(time.Now())
	if !ok || len(in.Segments) != 1 || in.Segments[0].ID != "later" {
		t.Fatal(in, b.View())
	}
}

func TestDisableDoesNotBypassPartialFreshness(t *testing.T) {
	b := New("c")
	b.Enable(true)
	b.Observe(seg("s", "system", "question"))
	ready(t, b)
	b.Observe(Segment{ID: "partial", Track: "system", At: time.Now().UTC().Format(time.RFC3339Nano)})
	b.Enable(false)
	if b.View().CanSend {
		t.Fatal("disable bypassed freshness")
	}
	b.Observe(seg("partial", "system", "additional condition"))
	b.Enable(true)
	b.Rebuild()
	in, ok := b.Begin(time.Now())
	if ok {
		_ = b.Apply(in, Result{HasQuestion: true, Ready: true, Text: "question", SourceIDs: []string{"s"}}, nil)
	}
	if b.View().CanSend {
		t.Fatal("rebuild hid missing paused speech")
	}
	_ = b.Edit("manually reviewed")
	_ = b.SaveEdit()
	if !b.View().CanSend {
		t.Fatal("manual exception blocked")
	}
}

func TestEditVisibleCardDoesNotConsumePendingAnalysis(t *testing.T) {
	b := New("c")
	b.Enable(true)
	b.Observe(seg("s", "system", "first"))
	ready(t, b)
	b.Observe(seg("extra", "system", "new condition"))
	_, _ = b.Begin(time.Now())
	_ = b.Edit("visible version")
	_ = b.SaveEdit()
	b.Sent("message")
	in, ok := b.Begin(time.Now())
	if !ok || len(in.Segments) != 1 || in.Segments[0].ID != "extra" {
		t.Fatal(in, b.View())
	}
}
func TestRejectedSourceUnionDoesNotAdvanceBoundary(t *testing.T) {
	b := New("c")
	b.Enable(true)
	b.current = Card{ID: "card", Text: "old", Sources: []Segment{}}
	for i := 0; i < 80; i++ {
		b.current.Sources = append(b.current.Sources, seg(string(rune(100+i)), "system", "old"))
	}
	b.Observe(seg("new", "system", "new condition"))
	in, _ := b.Begin(time.Now())
	if b.Apply(in, Result{HasQuestion: true, Ready: true, Text: "new", SourceIDs: []string{"new"}}, nil) == nil {
		t.Fatal("accepted 81 sources")
	}
	if b.processed == in.End || b.current.Text != "old" {
		t.Fatal("rejected condition consumed")
	}
}

func TestSourceTextBudgetRejectsUpdateWithoutConsumingConditions(t *testing.T) {
	b := New("c")
	b.Enable(true)
	b.Observe(seg("s", "system", strings.Repeat("я", 10000)))
	ready(t, b)
	before := b.View()
	b.Observe(seg("new", "system", strings.Repeat("x", 8000)))
	in, ok := b.Begin(time.Now())
	if !ok {
		t.Fatal(b.View())
	}
	err := b.Apply(in, Result{HasQuestion: true, Ready: true, Text: "updated", SourceIDs: []string{"new"}}, nil)
	if err == nil || b.processed == in.End || b.View().Text != before.Text {
		t.Fatal("oversized sources accepted or conditions consumed", err)
	}
}

func TestCardOriginTracksManualSaveAndExplicitRebuild(t *testing.T) {
	b := New("c")
	b.Enable(true)
	b.Observe(seg("s", "system", "question"))
	ready(t, b)
	if b.View().Origin != OriginAutomatic {
		t.Fatal(b.View())
	}
	if err := b.Edit("only reads"); err != nil {
		t.Fatal(err)
	}
	if err := b.SaveEdit(); err != nil {
		t.Fatal(err)
	}
	if b.View().Origin != OriginManual {
		t.Fatal(b.View())
	}
	b.Rebuild()
	ready(t, b)
	if b.View().Origin != OriginAutomatic {
		t.Fatal(b.View())
	}
}

func modelDraft(t *testing.T) *Builder {
	t.Helper()
	b := New("c")
	b.Enable(true)
	b.Observe(seg("s", "system", "Что такое map и как работает поиск? Потом обсудим ещё один момент."))
	in, ok := b.Begin(time.Now())
	if !ok {
		t.Fatal("no input")
	}
	if err := b.Apply(in, Result{HasQuestion: true, Ready: false, Text: "Что такое map и как работает поиск?", SourceIDs: []string{"s"}}, nil); err != nil {
		t.Fatal(err)
	}
	return b
}
func TestConfirmDraftUsesExplicitManualVersionAndRetainsTail(t *testing.T) {
	b := modelDraft(t)
	v := b.View()
	if v.CanSend || !v.CanConfirm {
		t.Fatal(v)
	}
	if err := b.Confirm(); err != nil {
		t.Fatal(err)
	}
	got := b.View()
	if !got.CanSend || got.CanConfirm || !got.Manual || got.Origin != OriginAutomatic || got.Version <= v.Version || got.Text != v.Text {
		t.Fatal(got)
	}
	b.Observe(seg("later", "system", "следующий вопрос"))
	if !b.View().CanSend {
		t.Fatal("confirmed manual version blocked")
	}
	b.Sent("message")
	in, ok := b.Begin(time.Now())
	if !ok || len(in.Segments) != 1 || in.Segments[0].ID != "later" {
		t.Fatal(in)
	}
}
func TestConfirmDraftCannotBypassFreshnessOrErrors(t *testing.T) {
	for _, state := range []string{"partial", "new-final", "analyzing", "error", "gap", "editing"} {
		t.Run(state, func(t *testing.T) {
			b := modelDraft(t)
			switch state {
			case "partial":
				b.Observe(Segment{ID: "p", Track: "system"})
			case "new-final":
				b.Observe(seg("new", "system", "ещё условие"))
			case "analyzing":
				b.Retry()
				_, _ = b.Begin(time.Now())
			case "error":
				b.Retry()
				in, _ := b.Begin(time.Now())
				_ = b.Apply(in, Result{}, errors.New("analysis failed"))
			case "gap":
				b.Observe(Segment{ID: "p", Track: "system"})
				b.Enable(false)
			case "editing":
				if err := b.Edit("edit"); err != nil {
					t.Fatal(err)
				}
			}
			before := b.View()
			if before.CanConfirm {
				t.Fatal("unsafe confirmation offered", before)
			}
			if err := b.Confirm(); err == nil {
				t.Fatal("unsafe confirmation accepted")
			}
			if after := b.View(); after.Manual || after.Text != before.Text || after.Version != before.Version {
				t.Fatal("rejected confirmation mutated card", after)
			}
		})
	}
}

func TestAutomaticQuestionRequiresFreshSubstantiveSystemEvidence(t *testing.T) {
	old := seg("old", "system", "Какие есть типы данных?")
	old.Seq = 1
	filler := seg("filler", "system", "Э-э… Угу.")
	filler.Seq = 2
	mic := seg("mic", "mic", "Про числовые типы?")
	mic.Seq = 3
	no := seg("no", "system", "Нет.")
	no.Seq = 4
	in := Input{Automatic: true, ProcessedThrough: 1, Segments: []Segment{old, filler, mic, no}}
	for _, tc := range []struct {
		name string
		ids  []string
		ok   bool
	}{
		{name: "processed", ids: []string{"old"}},
		{name: "hesitation", ids: []string{"filler"}},
		{name: "mic with old system speech", ids: []string{"old", "mic"}},
		{name: "meaningful short reply", ids: []string{"no"}, ok: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Result{HasQuestion: true, Ready: true, Text: "Какие есть типы данных?", SourceIDs: tc.ids}
			if err := Validate(in, r); (err == nil) != tc.ok {
				t.Fatalf("Validate = %v, accepted = %v", err, tc.ok)
			}
		})
	}
	in.Current = Card{ID: "draft", Text: old.Text, Sources: []Segment{old}}
	in.DraftCheck = true
	r := Result{HasQuestion: true, Ready: true, Text: old.Text, SourceIDs: []string{"old"}}
	if err := Validate(in, r); err != nil {
		t.Fatalf("draft check rejected its source: %v", err)
	}
}

func TestIndependentAutomaticQuestionDiscardsPreviousDraftSources(t *testing.T) {
	b := modelDraft(t)
	b.Observe(seg("new", "system", "Как работает GC?"))
	in, ok := b.Begin(time.Now())
	if !ok {
		t.Fatal("no input")
	}
	in.Automatic = true
	if err := b.Apply(in, Result{HasQuestion: true, Ready: true, Text: "Как работает GC?", SourceIDs: []string{"new"}}, nil); err != nil {
		t.Fatal(err)
	}
	if sources := b.View().Sources; len(sources) != 1 || sources[0].ID != "new" {
		t.Fatal(sources)
	}
}

func TestAutomaticFollowupUsesExplicitOlderTargetAndSentUpdatesItInPlace(t *testing.T) {
	b := New("c")
	b.RestoreRecent([]Card{{ID: "types", Text: "Какие есть типы данных?"}, {ID: "gc", Text: "Как работает GC?"}})
	b.Enable(true)
	b.Observe(seg("numbers", "system", "К первому вопросу: только числовые типы."))
	in, ok := b.Begin(time.Now())
	if !ok {
		t.Fatal("no input")
	}
	in.Automatic = true
	r := Result{HasQuestion: true, Ready: true, Text: "Какие есть числовые типы данных?", SourceIDs: []string{"numbers"}, FollowUp: true, Refinement: true, TargetQuestionID: "types"}
	if err := b.Apply(in, r, nil); err != nil {
		t.Fatal(err)
	}
	if v := b.View(); !v.CanSend || v.PreviousQuestionID != "types" {
		t.Fatal(v)
	}
	b.Sent("types")
	b.Observe(seg("next", "system", "К первому вопросу о вашем опыте: только про Go."))
	in, ok = b.Begin(time.Now())
	if !ok || len(in.Recent) != 2 || in.Recent[0].ID != "types" || in.Recent[0].Text != r.Text || in.Recent[1].ID != "gc" {
		t.Fatal(in.Recent)
	}
	in.Automatic = true
	in.Recent = append([]Card{{ID: "intro", Text: "Расскажите о себе"}}, in.Recent...)
	r.Text = "Расскажите о своём опыте работы с Go."
	r.SourceIDs = []string{"next"}
	r.TargetQuestionID = "intro"
	if err := b.Apply(in, r, nil); err != nil {
		t.Fatal(err)
	}
	if v := b.View(); !v.CanSend || v.PreviousQuestionID != "intro" {
		t.Fatal(v)
	}
	b.Sent("intro")
	b.Observe(seg("last", "system", "Следующий вопрос"))
	in, ok = b.Begin(time.Now())
	if !ok || len(in.Recent) != 2 || in.Recent[1].ID != "gc" {
		t.Fatal(in.Recent)
	}
}

func TestFollowupTargetMustBeExplicitInAutomaticMode(t *testing.T) {
	s := seg("s", "system", "Только числовые типы.")
	s.Seq = 1
	in := Input{Automatic: true, Segments: []Segment{s}, Recent: []Card{{ID: "types"}}}
	r := Result{HasQuestion: true, Ready: true, FollowUp: true, Text: "Какие есть числовые типы?", SourceIDs: []string{"s"}}
	if err := Validate(in, r); err == nil {
		t.Fatal("automatic follow-up chose a target implicitly")
	}
	r.TargetQuestionID = "other"
	if err := Validate(in, r); err == nil {
		t.Fatal("automatic follow-up accepted a foreign target")
	}
	in.Automatic = false
	r.TargetQuestionID = ""
	if err := Validate(in, r); err != nil {
		t.Fatalf("manual follow-up rejected the latest-question fallback: %v", err)
	}
}

func TestAutomaticContinuationKeepsPriorDraftSources(t *testing.T) {
	b := modelDraft(t)
	b.Observe(seg("new", "system", "В Go."))
	in, ok := b.Begin(time.Now())
	if !ok {
		t.Fatal("no input")
	}
	in.Automatic = true
	r := Result{HasQuestion: true, Ready: true, Text: "Что такое map и как работает поиск в Go?", SourceIDs: []string{"new"}, ContinuesDraft: true}
	if err := b.Apply(in, r, nil); err != nil {
		t.Fatal(err)
	}
	if sources := b.View().Sources; len(sources) != 2 || sources[0].ID != "s" || sources[1].ID != "new" {
		t.Fatal(sources)
	}
}

func TestAutomaticContinuationRejectsUnrelatedProcessedEvidence(t *testing.T) {
	old := seg("old", "system", "Как работает GC?")
	old.Seq = 1
	draft := seg("draft", "system", "Какие есть типы данных в…")
	draft.Seq = 2
	fresh := seg("fresh", "system", "в Go")
	fresh.Seq = 3
	in := Input{
		Automatic: true, ProcessedThrough: 2, Segments: []Segment{old, draft, fresh},
		Current: Card{ID: "pending", Text: draft.Text, Sources: []Segment{draft}},
	}
	r := Result{HasQuestion: true, Ready: true, ContinuesDraft: true, Text: "Какие есть типы данных в Go?", SourceIDs: []string{"old", "fresh"}}
	if err := Validate(in, r); err == nil {
		t.Fatal("accepted processed evidence from another question")
	}
	r.SourceIDs = []string{"draft", "fresh"}
	if err := Validate(in, r); err != nil {
		t.Fatalf("rejected prior draft evidence: %v", err)
	}
}

func TestAdditionalAutomaticRefinementKeepsItsTargetAndPosition(t *testing.T) {
	b := New("c")
	b.RestoreRecent([]Card{{ID: "types", Text: "Какие есть типы данных?"}})
	b.Enable(true)
	b.Observe(seg("batch", "system", "Как работает GC? А в вопросе про типы данных только числовые."))
	in, ok := b.Begin(time.Now())
	if !ok {
		t.Fatal("no input")
	}
	in.Automatic = true
	r := Result{
		HasQuestion: true, Ready: true, Text: "Как работает GC?", SourceIDs: []string{"batch"},
		Additional: []AdditionalQuestion{{
			Text: "Какие есть числовые типы данных?", SourceIDs: []string{"batch"},
			TargetQuestionID: "types", Refinement: true,
		}},
	}
	withoutTarget := r
	withoutTarget.Additional = []AdditionalQuestion{{Text: r.Additional[0].Text, SourceIDs: []string{"batch"}, Refinement: true}}
	if err := Validate(in, withoutTarget); err == nil {
		t.Fatal("accepted additional refinement with no target")
	}
	if err := b.Apply(in, r, nil); err != nil {
		t.Fatal(err)
	}
	if v := b.View(); v.Text != r.Text || v.Refinement {
		t.Fatal(v)
	}
	b.Sent("gc")
	if v := b.View(); !v.CanSend || v.Text != r.Additional[0].Text || !v.Refinement || v.PreviousQuestionID != "types" {
		t.Fatal(v)
	}
}

func TestRepeatedRefinementDoesNotBlockAdditionalIndependentQuestion(t *testing.T) {
	b := New("c")
	b.RestoreRecent([]Card{{ID: "types", Text: "Какие есть типы данных?"}})
	b.Enable(true)
	b.Observe(seg("repeat", "system", "Какие есть типы данных?"))
	b.Observe(seg("different", "system", "Как работает GC?"))
	in, ok := b.Begin(time.Now())
	if !ok {
		t.Fatal("no input")
	}
	in.Automatic = true
	r := Result{
		HasQuestion: true, Ready: true, Text: "Какие есть типы данных?", SourceIDs: []string{"repeat"},
		FollowUp: true, Refinement: true, TargetQuestionID: "types",
		Additional: []AdditionalQuestion{{Text: "Как работает GC?", SourceIDs: []string{"different"}}},
	}
	if err := b.Apply(in, r, nil); err != nil {
		t.Fatal(err)
	}
	if v := b.View(); !v.CanSend || v.Refinement || v.Text != "Как работает GC?" {
		t.Fatal(v)
	}
}

func TestIsFillerPreservesShortMeaningfulSpeech(t *testing.T) {
	for _, phrase := range []string{"", "…?!", "Э-э… Угу.", "[кашель] (пауза) Ага.", "[noise] um, okay"} {
		if !IsFiller(phrase) {
			t.Errorf("substantive: %q", phrase)
		}
	}
	for _, phrase := range []string{"Да.", "Нет.", "42", "Go", "C++", "Угу, только на Linux", "[noise] 2", "[i]", "а…"} {
		if IsFiller(phrase) {
			t.Errorf("filler: %q", phrase)
		}
	}
}

func TestAnalysisRetryRequiresSubstantiveSystemFinalAfterFailedSnapshot(t *testing.T) {
	b := New("c")
	b.Enable(true)
	b.Observe(seg("question", "system", "Что такое GC?"))
	in, ok := b.Begin(time.Now())
	if !ok {
		t.Fatal("no input")
	}
	b.Observe(seg("mic", "mic", "Другой вопрос"))
	b.Observe(seg("filler", "system", "Э-э… Угу."))
	_ = b.Apply(in, Result{}, errors.New("analysis failed"))
	if b.RetryIfNewSystemSpeech(in.End) || b.View().Error == "" {
		t.Fatal("retried without substantive system speech")
	}
	b.Observe(seg("tail", "system", "А в Go?"))
	if !b.RetryIfNewSystemSpeech(in.End) || b.View().Error != "" {
		t.Fatal("did not retry after new system speech")
	}
	next, ok := b.Begin(time.Now())
	if !ok || next.End <= in.End {
		t.Fatal(next)
	}
	_ = b.Apply(next, Result{}, errors.New("analysis failed again"))
	if b.RetryIfNewSystemSpeech(next.End) {
		t.Fatal("retried the same snapshot")
	}
}
