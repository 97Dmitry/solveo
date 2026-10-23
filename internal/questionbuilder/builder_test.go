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
