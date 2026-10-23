package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/audiohistory"
	"github.com/97Dmitry/solveo/internal/stt"
)

func TestChatAudioHistoryMergesStopsAndReloadsWithoutStartingSTT(t *testing.T) {
	archive := audiohistory.New(t.TempDir())
	h := setupAudioHistory(t, archive)
	h.listen(t)
	chatID := h.c.Snapshot().ActiveChatID
	for i := 0; i < 3; i++ {
		if i > 0 {
			if err := h.c.StartListening(); err != nil {
				t.Fatal(err)
			}
		}
		h.c.audio.mu.Lock()
		stream := h.c.audio.sources[0].stream
		h.c.audio.mu.Unlock()
		h.c.applyAudioUpdate(stt.Update{StreamID: stream, UtteranceID: stream + ":1", Kind: stt.KindFinal, Revision: 1, Text: "единая история"})
		if err := h.c.StopListening(); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored := setupAudioHistory(t, archive)
	doc, err := restored.c.LoadChatAudioHistory(chatID)
	if err != nil || len(doc.Segments) != 3 || doc.ChatID != chatID {
		t.Fatal(doc, err)
	}
	if v := restored.c.AudioSnapshot(); v.Listening || v.Model != audioModelStopped {
		t.Fatal("history restarted recognition")
	}
}
func TestChatAudioHistorySwitchKeepsStartedUtteranceWithOldChat(t *testing.T) {
	archive := audiohistory.New(t.TempDir())
	h := setupAudioHistory(t, archive)
	h.listen(t)
	first := h.c.Snapshot().ActiveChatID
	stream := h.session.stream("system")
	h.c.applyAudioUpdate(stt.Update{StreamID: stream, UtteranceID: stream + ":old", Kind: stt.KindPartial, Revision: 1, Text: "начатый вопрос"})
	second, err := h.c.NewChat()
	if err != nil {
		t.Fatal(err)
	}
	if len(h.c.AudioSnapshot().Segments) != 0 {
		t.Fatal("old phrases leaked to new chat")
	}
	h.c.applyAudioUpdate(stt.Update{StreamID: stream, UtteranceID: stream + ":old", Kind: stt.KindFinal, Revision: 2, Text: "вопрос старого чата"})
	h.c.applyAudioUpdate(stt.Update{StreamID: stream, UtteranceID: stream + ":new", Kind: stt.KindFinal, Revision: 1, Text: "вопрос нового чата"})
	live := h.c.AudioSnapshot()
	if len(live.Segments) != 1 || live.Segments[0].Text != "вопрос нового чата" || !live.Listening {
		t.Fatal(live)
	}
	oldDoc, err := h.c.LoadChatAudioHistory(first)
	if err != nil || len(oldDoc.Segments) != 1 || oldDoc.Segments[0].Text != "вопрос старого чата" {
		t.Fatal(oldDoc, err)
	}
	newDoc, err := h.c.LoadChatAudioHistory(second.ActiveChatID)
	if err != nil || len(newDoc.Segments) != 1 || newDoc.Segments[0].Text != "вопрос нового чата" {
		t.Fatal(newDoc, err)
	}
	h.session.mu.Lock()
	opens := len(h.session.opens)
	h.session.mu.Unlock()
	if opens != 2 {
		t.Fatal("chat switch reopened STT streams")
	}
}
func TestShutdownDoesNotFlushStoppedStreamsAgain(t *testing.T) {
	h := setupAudio(t)
	h.listen(t)
	if err := h.c.StopListening(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		h.session.mu.Lock()
		closed := len(h.session.closedSt)
		h.session.mu.Unlock()
		if closed == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := h.c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.session.mu.Lock()
	count := len(h.session.flushed)
	h.session.mu.Unlock()
	if count != 2 {
		t.Fatalf("stopped streams flushed %d times", count)
	}
}

func TestChatAudioHistoryKeepsReadablePartsWhenUnrelatedFileIsBroken(t *testing.T) {
	dir := t.TempDir()
	archive := audiohistory.New(dir)
	h := setupAudioHistory(t, archive)
	h.listen(t)
	h.finals(t, "доступная история")
	if err := h.c.StopListening(); err != nil {
		t.Fatal(err)
	}
	if err := h.c.flushAudioHistory(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ffffffffffffffffffffffffffffffff.json"), []byte(`{"version":99}`), 0600); err != nil {
		t.Fatal(err)
	}
	doc, err := h.c.LoadChatAudioHistory(h.c.Snapshot().ActiveChatID)
	if err != nil || len(doc.Segments) != 1 || doc.Notice == "" {
		t.Fatal("valid history hidden or incomplete archive not marked", doc, err)
	}
}

func TestChatAudioHistoryAttachAndDeletePreserveOtherChats(t *testing.T) {
	archive := audiohistory.New(t.TempDir())
	h := setupAudioHistory(t, archive)
	first := h.c.Snapshot().ActiveChatID
	second, err := h.c.NewChat()
	if err != nil {
		t.Fatal(err)
	}
	for i, owner := range []string{first, second.ActiveChatID, ""} {
		id := []string{"11111111111111111111111111111111", "22222222222222222222222222222222", "33333333333333333333333333333333"}[i]
		now := time.Now().UTC()
		if err := archive.Save(audiohistory.Session{ID: id, ChatID: owner, ProviderID: "qwen", ProviderLabel: "Qwen", StartedAt: now, EndedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.c.AttachAudioHistory("33333333333333333333333333333333", first); err != nil {
		t.Fatal(err)
	}
	parts, err := archive.ForChat(first)
	if err != nil || len(parts) != 2 {
		t.Fatal(parts, err)
	}
	if err := h.c.DeleteChatAudioHistory(first); err != nil {
		t.Fatal(err)
	}
	parts, err = archive.ForChat(first)
	if err != nil || len(parts) != 0 {
		t.Fatal(parts, err)
	}
	parts, err = archive.ForChat(second.ActiveChatID)
	if err != nil || len(parts) != 1 {
		t.Fatal("deleting one chat affected another", parts, err)
	}
}
