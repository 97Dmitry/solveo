package history

import (
	"testing"

	"github.com/97Dmitry/solveo/internal/chat"
)

func TestReferenceArchiveRejectsFutureAndUnfinishedTargets(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	doc := chat.New()
	answer := chat.Message{ID: chat.ID(), Role: chat.RoleAssistant, Status: chat.StatusComplete, Text: "selected"}
	question := chat.Message{ID: chat.ID(), Role: chat.RoleUser, Status: chat.StatusComplete, Text: "shorter", ReplyToID: answer.ID}
	doc.Messages = []chat.Message{answer, question}
	if _, err = store.Save(SaveInput{Chat: doc}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(doc.ID)
	if err != nil || got.Messages[1].ReplyToID != answer.ID {
		t.Fatalf("%+v %v", got, err)
	}
	doc.Messages = []chat.Message{question, answer}
	if _, err = store.Save(SaveInput{Chat: doc}); err == nil {
		t.Fatal("future reference accepted")
	}
	answer.Status = chat.StatusInterrupted
	doc.Messages = []chat.Message{answer, question}
	if _, err = store.Save(SaveInput{Chat: doc}); err == nil {
		t.Fatal("incomplete reference accepted")
	}
}
