package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/97Dmitry/solveo/internal/chat"
)

func TestFollowupPersistsReferenceAndPreservesNextDraft(t *testing.T) {
	c, f, store := setup(t)
	id := c.Snapshot().ActiveChatID
	_, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: id, Text: "first"})
	if err != nil {
		t.Fatal(err)
	}
	<-f.started
	f.finish <- nil
	waitIdle(t, c)
	answer := c.Snapshot().Messages[1]
	in := SubmitInput{RequestID: chat.ID(), ChatID: id, Text: "shorter", DraftRevision: 2, ReplyToID: answer.ID}
	if _, err = c.Submit(in); err != nil {
		t.Fatal(err)
	}
	req := <-f.started
	if req.Turn.ReplyToID != answer.ID || req.Turn.Text != "shorter" {
		t.Fatalf("%+v", req.Turn)
	}
	if err = c.SetDraft(DraftInput{ChatID: id, Text: "next question", Revision: 3}); err != nil {
		t.Fatal(err)
	}
	f.finish <- errors.New("offline")
	waitIdle(t, c)
	if c.Snapshot().Draft.Text != "next question" {
		t.Fatal("next draft lost")
	}
	doc, err := store.Load(id)
	if err != nil || doc.Messages[2].ReplyToID != answer.ID {
		t.Fatalf("%+v %v", doc, err)
	}
	if _, err = c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: id, RetryMessageID: doc.Messages[2].ID, ReplyToID: "invalid"}); err != nil {
		t.Fatal(err)
	}
	retry := <-f.started
	if retry.Turn.ReplyToID != answer.ID {
		t.Fatal("retry changed reference")
	}
	f.finish <- nil
	waitIdle(t, c)
	if c.Snapshot().Draft.Text != "next question" {
		t.Fatal("retry cleared next draft")
	}
}

func TestFollowupRejectsMissingAnswerBeforeProvider(t *testing.T) {
	c, f, _ := setup(t)
	id := c.Snapshot().ActiveChatID
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: id, Text: "shorter", ReplyToID: "foreign"}); err == nil {
		t.Fatal("accepted missing answer")
	}
	if err := c.SetDraft(DraftInput{ChatID: id, Text: "shorter", Revision: 1, ReplyToID: "foreign"}); err == nil {
		t.Fatal("accepted missing draft target")
	}
	if len(c.Snapshot().Messages) != 0 || f.calls != 0 {
		t.Fatal("invalid reference changed conversation")
	}
}

func TestFollowupDraftSurvivesRestart(t *testing.T) {
	c, f, _ := setup(t)
	id := c.Snapshot().ActiveChatID
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: id, Text: "first"}); err != nil {
		t.Fatal(err)
	}
	<-f.started
	f.finish <- nil
	waitIdle(t, c)
	answer := c.Snapshot().Messages[1]
	// A selected reference with no text is unfinished work too.
	if err := c.SetDraft(DraftInput{ChatID: id, Revision: 5, ReplyToID: answer.ID}); err != nil {
		t.Fatal(err)
	}
	persisted(t, c)
	crash(c)
	next, provider := reopen(t, c)
	if _, err := next.OpenChat(id); err != nil {
		t.Fatal(err)
	}
	if next.Snapshot().Draft.ReplyToID != answer.ID {
		t.Fatalf("%+v", next.Snapshot().Draft)
	}
	noProviderCall(t, provider)
}

func TestFollowupRestoreKeepsReferenceWhenArchiveCannotBeRead(t *testing.T) {
	c, f, store := setup(t)
	id := c.Snapshot().ActiveChatID
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: id, Text: "first"}); err != nil {
		t.Fatal(err)
	}
	<-f.started
	f.finish <- nil
	waitIdle(t, c)
	target := c.Snapshot().Messages[1].ID
	if err := c.SetDraft(DraftInput{ChatID: id, Revision: 5, Text: "shorter", ReplyToID: target}); err != nil {
		t.Fatal(err)
	}
	persisted(t, c)
	crash(c)
	if err := os.WriteFile(filepath.Join(store.Dir, "chats", id+".json"), []byte("unreadable document"), 0600); err != nil {
		t.Fatal(err)
	}
	next, _ := reopen(t, c)
	persisted(t, next)
	saved, _, err := next.deps.Work.Load()
	if err != nil || saved.Chats[id].Draft.ReplyToID != target {
		t.Fatalf("reference lost on archive read error: %+v %v", saved, err)
	}
}
