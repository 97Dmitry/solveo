package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/questionbuilder"
	"github.com/97Dmitry/solveo/internal/stt"
)

func lastUserText(c *Controller) string {
	text := ""
	for _, m := range c.Snapshot().Messages {
		if m.Role == "user" {
			text = m.Text
		}
	}
	return text
}

func TestSendUnsentSendsInterviewerPhrasesAfterTheLastSentOne(t *testing.T) {
	h := setupAudio(t)
	chatID := h.c.Snapshot().ActiveChatID
	h.listen(t)
	ids := h.finals(t, "Старый вопрос.", "Расскажите про каналы.")
	if _, err := h.c.SubmitTranscript(SubmitTranscriptInput{ChatID: chatID, RequestID: "selected", SegmentIDs: ids[:1]}); err != nil {
		t.Fatal(err)
	}
	<-h.provider.started
	h.provider.finish <- nil
	waitIdle(t, h.c)
	h.update("mic", stt.KindFinal, 1, 1, "кандидат думает")
	h.update("system", stt.KindFinal, 3, 1, "И про select.")
	waitAudio(t, h.c, "new phrases", func(v AudioView) bool { return len(v.Segments) == 4 })
	if err := h.c.SendUnsentTranscript(chatID); err != nil {
		t.Fatal(err)
	}
	req := <-h.provider.started
	h.provider.finish <- nil
	waitIdle(t, h.c)
	if !req.Turn.FromTranscript || lastUserText(h.c) != "Расскажите про каналы.\nИ про select." {
		t.Fatalf("sent %q", lastUserText(h.c))
	}
	v := waitAudio(t, h.c, "sent marks", func(v AudioView) bool { return !v.SendingUnsent })
	for _, s := range v.Segments {
		if s.Sent != (s.Track == "system") {
			t.Fatalf("sent marks %+v", v.Segments)
		}
	}
	if err := h.c.SendUnsentTranscript(chatID); err != errNoUnsent {
		t.Fatal("repeated send", err)
	}
	if err := h.c.SendUnsentTranscript("stale"); err != ErrStale {
		t.Fatal(err)
	}
}

func TestSendUnsentWaitsForThePhraseBeingRecognized(t *testing.T) {
	h := setupAudio(t)
	chatID := h.c.Snapshot().ActiveChatID
	h.listen(t)
	h.finals(t, "Что такое горутина?")
	h.update("system", stt.KindStarted, 2, 0, "")
	h.update("system", stt.KindPartial, 2, 1, "И чем она")
	waitAudio(t, h.c, "partial", func(v AudioView) bool { return len(v.Segments) == 2 })
	if err := h.c.SendUnsentTranscript(chatID); err != nil {
		t.Fatal(err)
	}
	waitAudio(t, h.c, "waiting", func(v AudioView) bool { return v.SendingUnsent })
	if err := h.c.SendUnsentTranscript(chatID); err == nil {
		t.Fatal("second concurrent send accepted")
	}
	// Speech that starts after the press is not part of this question.
	h.update("system", stt.KindStarted, 3, 0, "")
	h.update("system", stt.KindFinal, 2, 2, "И чем она отличается от потока?")
	req := <-h.provider.started
	h.provider.finish <- nil
	waitIdle(t, h.c)
	if !strings.Contains(req.Turn.Text, "отличается от потока?") || lastUserText(h.c) != "Что такое горутина?\nИ чем она отличается от потока?" {
		t.Fatalf("sent %q", lastUserText(h.c))
	}
	h.update("system", stt.KindFinal, 3, 1, "Следующий вопрос.")
	v := waitAudio(t, h.c, "late phrase", func(v AudioView) bool { return len(v.Segments) == 3 && !v.SendingUnsent })
	if v.Segments[2].Sent {
		t.Fatal("phrase started after the press was marked sent")
	}
}

func TestSendUnsentTimeoutNeverSendsAPartial(t *testing.T) {
	h := setupAudio(t)
	chatID := h.c.Snapshot().ActiveChatID
	old := unsentFinalWait
	unsentFinalWait = 50 * time.Millisecond
	t.Cleanup(func() { unsentFinalWait = old })
	h.listen(t)
	h.update("system", stt.KindStarted, 1, 0, "")
	h.update("system", stt.KindPartial, 1, 1, "ещё говорят")
	waitAudio(t, h.c, "partial", func(v AudioView) bool { return len(v.Segments) == 1 })
	if err := h.c.SendUnsentTranscript(chatID); err != nil {
		t.Fatal(err)
	}
	v := waitAudio(t, h.c, "timeout", func(v AudioView) bool { return !v.SendingUnsent && v.SendError != "" })
	if !strings.Contains(v.SendError, "ещё распознаётся") || !strings.Contains(h.c.Snapshot().Error, "ещё распознаётся") {
		t.Fatal(v.SendError, h.c.Snapshot().Error)
	}
	select {
	case r := <-h.provider.started:
		t.Fatal("partial sent", r.Turn.Text)
	default:
	}
}

func TestSendUnsentConsumesTheAutomaticCardBuiltFromThesePhrases(t *testing.T) {
	h := setupAudio(t)
	chatID := h.c.Snapshot().ActiveChatID
	h.listen(t)
	h.c.mu.Lock()
	b := h.c.questionLocked()
	b.Enable(true)
	h.c.mu.Unlock()
	ids := h.finals(t, "Объясните мьютекс.")
	h.c.mu.Lock()
	if h.c.question.timer != nil {
		h.c.question.timer.Stop()
	}
	in, ok := b.Begin(time.Now())
	if !ok {
		h.c.mu.Unlock()
		t.Fatal("no builder input")
	}
	err := b.Apply(in, questionbuilder.Result{HasQuestion: true, Ready: true, Text: "Объясните мьютекс", SourceIDs: ids}, nil)
	h.c.mu.Unlock()
	if err != nil || h.c.QuestionSnapshot().Text == "" {
		t.Fatal("card not built", err)
	}
	if err = h.c.SendUnsentTranscript(chatID); err != nil {
		t.Fatal(err)
	}
	req := <-h.provider.started
	h.provider.finish <- nil
	waitIdle(t, h.c)
	if got := h.c.QuestionSnapshot(); got.Text != "" || got.ID != "" {
		t.Fatalf("card survived the direct send: %+v", got)
	}
	h.update("system", stt.KindFinal, 2, 1, "А если два мьютекса?")
	waitAudio(t, h.c, "follow-up phrase", func(v AudioView) bool { return len(v.Segments) == 2 })
	h.c.mu.Lock()
	if h.c.question.timer != nil {
		h.c.question.timer.Stop()
	}
	next, ok := b.Begin(time.Now())
	h.c.mu.Unlock()
	if !ok || len(next.Recent) != 1 || next.Recent[0].ID != req.Turn.ID || len(next.Segments) != 1 || next.Segments[0].ID != fmt.Sprintf("%s:2", h.session.stream("system")) {
		t.Fatalf("builder input after direct send: %+v", next)
	}
}
