package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/audiohistory"
	"github.com/97Dmitry/solveo/internal/stt"
)

func TestAudioHistorySurvivesRestartClearAndDisplayEviction(t *testing.T) {
	archive := audiohistory.New(t.TempDir())
	h := setupAudioHistory(t, archive)
	h.listen(t)
	h.update("mic", stt.KindPartial, 1, 1, "неподтверждено")
	for i := 1; i <= 350; i++ {
		h.update("system", stt.KindFinal, i, 1, fmt.Sprint(i))
	}
	waitAudio(t, h.c, "all finals", func(v AudioView) bool { return len(v.Segments) == 300 && v.Segments[299].Text == "350" })
	if err := h.c.ClearTranscript(); err != nil {
		t.Fatal(err)
	}
	if err := h.c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	h2 := setupAudioHistory(t, archive)
	list, err := h2.c.ListAudioHistory()
	if err != nil || len(list.Sessions) != 1 {
		t.Fatal(list, err)
	}
	doc, err := h2.c.LoadAudioHistory(list.Sessions[0].ID)
	if err != nil || len(doc.Segments) != 350 || doc.Segments[0].Text != "1" || doc.Segments[349].Text != "350" || doc.EndedAt == "" {
		t.Fatal(len(doc.Segments), doc.EndedAt, err)
	}
	if v := h2.c.AudioSnapshot(); v.Listening || len(v.Segments) != 0 || v.Model != audioModelStopped {
		t.Fatal("archive load changed live state", v)
	}
}
func TestAudioHistoryLateFinalStaysWithOriginalSession(t *testing.T) {
	archive := audiohistory.New(t.TempDir())
	h := setupAudioHistory(t, archive)
	h.listen(t)
	oldStream := h.session.stream("system")
	h.c.audio.mu.Lock()
	oldID := h.c.audio.history.current
	h.c.audio.mu.Unlock()
	if err := h.c.StopListening(); err != nil {
		t.Fatal(err)
	}
	if err := h.c.StartListening(); err != nil {
		t.Fatal(err)
	}
	h.c.audio.mu.Lock()
	newID := h.c.audio.history.current
	h.c.audio.mu.Unlock()
	h.session.events <- stt.Update{StreamID: oldStream, UtteranceID: oldStream + ":late", Kind: stt.KindFinal, Revision: 1, Text: "поздний финал"}
	waitAudio(t, h.c, "late final", func(v AudioView) bool { return len(v.Segments) == 1 })
	if err := h.c.flushAudioHistory(); err != nil {
		t.Fatal(err)
	}
	old, err := archive.Load(oldID)
	if err != nil || len(old.Segments) != 1 {
		t.Fatal(old, err)
	}
	newer, err := archive.Load(newID)
	if err != nil || len(newer.Segments) != 0 {
		t.Fatal(newer, err)
	}
}
func TestAudioHistoryTracksSubmissionAndProtectsActiveDeletion(t *testing.T) {
	archive := audiohistory.New(t.TempDir())
	h := setupAudioHistory(t, archive)
	h.listen(t)
	ids := h.finals(t, "Почему индекс мешает записи?")
	h.c.audio.mu.Lock()
	id := h.c.audio.history.current
	h.c.audio.mu.Unlock()
	if err := h.c.DeleteAudioHistory(id); err == nil {
		t.Fatal("active history deleted")
	}
	chatID := h.c.Snapshot().ActiveChatID
	op, err := h.c.SubmitTranscript(SubmitTranscriptInput{ChatID: chatID, RequestID: "hist", SegmentIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	<-h.provider.started
	h.provider.finish <- nil
	waitIdle(t, h.c)
	if err := h.c.flushAudioHistory(); err != nil {
		t.Fatal(err)
	}
	doc, err := archive.Load(id)
	if err != nil || len(doc.Submissions) != 1 || doc.Submissions[0].ChatID != chatID || doc.Submissions[0].OperationID != op {
		t.Fatal(doc.Submissions, err)
	}
	if err := h.c.StopListening(); err != nil {
		t.Fatal(err)
	}
	if err := h.c.DeleteAudioHistory(id); err != nil {
		t.Fatal(err)
	}
	h.update("system", stt.KindFinal, 100, 1, "после удаления")
	waitAudio(t, h.c, "deleted late", func(v AudioView) bool { return len(v.Segments) == 2 })
	if err := h.c.flushAudioHistory(); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Load(id); err == nil {
		t.Fatal("late final recreated deleted history")
	}
}

type failingAudioHistory struct {
	AudioHistoryStore
	mu   sync.Mutex
	fail bool
}

func (f *failingAudioHistory) Save(s audiohistory.Session) error {
	f.mu.Lock()
	fail := f.fail
	f.mu.Unlock()
	if fail {
		return errors.New("disk unavailable")
	}
	return f.AudioHistoryStore.Save(s)
}
func TestAudioHistoryFailureRemainsVisibleAndCloseCanRetry(t *testing.T) {
	f := &failingAudioHistory{AudioHistoryStore: audiohistory.New(t.TempDir()), fail: true}
	h := setupAudioHistory(t, f)
	h.listen(t)
	h.finals(t, "не терять финал")
	if err := h.c.flushAudioHistory(); err == nil {
		t.Fatal("failed write accepted")
	}
	if h.c.AudioSnapshot().HistoryError == "" {
		t.Fatal("write error hidden")
	}
	if err := h.c.Close(context.Background()); err == nil {
		t.Fatal("close claimed unsaved history was saved")
	}
	f.mu.Lock()
	f.fail = false
	f.mu.Unlock()
	if err := h.c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, err := f.List()
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	doc, err := f.Load(list[0].ID)
	if err != nil || len(doc.Segments) != 1 {
		t.Fatal(doc, err)
	}
}
func TestAudioHistoryEmptyFinalIsNotArchived(t *testing.T) {
	archive := audiohistory.New(t.TempDir())
	h := setupAudioHistory(t, archive)
	h.listen(t)
	h.update("mic", stt.KindPartial, 1, 1, "не подтверждено")
	h.update("mic", stt.KindFinal, 1, 2, "")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		h.c.audio.mu.Lock()
		done := h.c.audio.revisions[h.session.stream("mic")+":1"] == 0
		h.c.audio.mu.Unlock()
		if done {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := h.c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, err := archive.List()
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	doc, err := archive.Load(list[0].ID)
	if err != nil || len(doc.Segments) != 0 {
		t.Fatal(doc, err)
	}
}

func TestAudioHistorySegmentLimitDoesNotDiscardDiagnostics(t *testing.T) {
	archive := audiohistory.New(t.TempDir())
	h := setupAudioHistory(t, archive)
	h.listen(t)
	ids := h.finals(t, "первая")
	stream := h.session.stream("system")
	h.c.audio.mu.Lock()
	id := h.c.audio.history.current
	doc := h.c.audio.history.docs[id]
	first := doc.session.Segments[0]
	for i := 2; i <= audiohistory.MaxSegments; i++ {
		r := first
		r.ID = fmt.Sprintf("%s:%d", stream, i)
		doc.session.Segments = append(doc.session.Segments, r)
	}
	h.c.audio.mu.Unlock()
	chatID := h.c.Snapshot().ActiveChatID
	if _, err := h.c.SubmitTranscript(SubmitTranscriptInput{ChatID: chatID, RequestID: "limit", SegmentIDs: ids}); err != nil {
		t.Fatal(err)
	}
	<-h.provider.started
	h.provider.finish <- nil
	waitIdle(t, h.c)
	h.c.audio.mu.Lock()
	h.c.recordAudioErrorLocked(stream, "проверка источника")
	h.c.audio.mu.Unlock()
	if err := h.c.flushAudioHistory(); err != nil {
		t.Fatal(err)
	}
	saved, err := archive.Load(id)
	if err != nil || len(saved.Submissions) != 1 {
		t.Fatal("submission lost at segment limit", err)
	}
	found := false
	for _, e := range saved.Errors {
		found = found || e.Message == "проверка источника"
	}
	if !found {
		t.Fatal("source error lost at segment limit")
	}
}
func TestAudioHistoryReleasesSavedSessionsAndReloadsLateFinal(t *testing.T) {
	archive := audiohistory.New(t.TempDir())
	h := setupAudioHistory(t, archive)
	h.listen(t)
	var oldestID, oldestStream string
	for i := 0; i < 10; i++ {
		if i > 0 {
			if err := h.c.StartListening(); err != nil {
				t.Fatal(err)
			}
		}
		h.c.audio.mu.Lock()
		id := h.c.audio.history.current
		stream := h.c.audio.sources[0].stream
		h.c.audio.mu.Unlock()
		if i == 0 {
			oldestID, oldestStream = id, stream
		}
		h.c.applyAudioUpdate(stt.Update{StreamID: stream, UtteranceID: stream + ":1", Kind: stt.KindFinal, Revision: 1, Text: "сохранённая фраза"})
		if err := h.c.StopListening(); err != nil {
			t.Fatal(err)
		}
		if err := h.c.flushAudioHistory(); err != nil {
			t.Fatal(err)
		}
	}
	h.c.audio.mu.Lock()
	resident := len(h.c.audio.history.docs)
	h.c.audio.mu.Unlock()
	if resident > 4 {
		t.Fatalf("%d completed sessions retained", resident)
	}
	h.c.applyAudioUpdate(stt.Update{StreamID: oldestStream, UtteranceID: oldestStream + ":2", Kind: stt.KindFinal, Revision: 1, Text: "поздний финал после выгрузки"})
	if err := h.c.flushAudioHistory(); err != nil {
		t.Fatal(err)
	}
	saved, err := archive.Load(oldestID)
	if err != nil || len(saved.Segments) != 2 {
		t.Fatal("late final lost after eviction", err)
	}
}

func TestAudioHistorySavesFinalDeliveredDuringShutdownFlush(t *testing.T) {
	archive := audiohistory.New(t.TempDir())
	h := setupAudioHistory(t, archive)
	h.listen(t)
	stream := h.session.stream("system")
	h.session.mu.Lock()
	h.session.flushHook = func(id string) {
		if id == stream {
			h.session.events <- stt.Update{StreamID: stream, UtteranceID: stream + ":tail", Kind: stt.KindFinal, Revision: 1, Text: "последняя фраза при выходе"}
		}
	}
	h.session.mu.Unlock()
	if err := h.c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, err := archive.List()
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	saved, err := archive.Load(list[0].ID)
	if err != nil || len(saved.Segments) != 1 || saved.Segments[0].Text != "последняя фраза при выходе" {
		t.Fatal(saved, err)
	}
}
func TestAudioHistorySubmissionPinsMultipleEvictedSessions(t *testing.T) {
	archive := audiohistory.New(t.TempDir())
	h := setupAudioHistory(t, archive)
	h.listen(t)
	groups := map[string][]string{}
	for i := 0; i < 10; i++ {
		if i > 0 {
			if err := h.c.StartListening(); err != nil {
				t.Fatal(err)
			}
		}
		h.c.audio.mu.Lock()
		id := h.c.audio.history.current
		stream := h.c.audio.sources[0].stream
		h.c.audio.mu.Unlock()
		row := stream + ":1"
		h.c.applyAudioUpdate(stt.Update{StreamID: stream, UtteranceID: row, Kind: stt.KindFinal, Revision: 1, Text: "из разных сеансов"})
		groups[id] = []string{row}
		if err := h.c.StopListening(); err != nil {
			t.Fatal(err)
		}
		if err := h.c.flushAudioHistory(); err != nil {
			t.Fatal(err)
		}
	}
	h.c.recordAudioSubmission(groups, "11111111111111111111111111111111", "22222222222222222222222222222222")
	if err := h.c.flushAudioHistory(); err != nil {
		t.Fatal(err)
	}
	for id := range groups {
		saved, err := archive.Load(id)
		if err != nil || len(saved.Submissions) != 1 {
			t.Fatalf("session %s lost submission: %v", id, err)
		}
	}
}
