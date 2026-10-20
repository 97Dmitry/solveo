package chat

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestCloneSharesNoMutableData(t *testing.T) {
	src := New()
	src.ProviderState = json.RawMessage(`{"version":1}`)
	src.Attachments = []Attachment{{ID: "a1", Path: "images/x/a1.png"}}
	src.Messages = []Message{{
		ID:            "m1",
		Role:          "user",
		Text:          "question",
		Status:        "complete",
		AttachmentIDs: []string{"a1"},
		Attempts:      []Attempt{{ID: "t1", Status: "complete"}},
	}}
	want := Clone(src)
	if !reflect.DeepEqual(want, src) {
		t.Fatalf("clone differs from source:\n%+v\n%+v", want, src)
	}

	got := Clone(src)
	got.ProviderState[2] = 'X'
	got.Attachments[0].Path = "changed"
	got.Messages[0].Text = "changed"
	got.Messages[0].AttachmentIDs[0] = "changed"
	got.Messages[0].Attempts[0].Status = "changed"
	got.Messages = append(got.Messages, Message{ID: "m2"})
	if !reflect.DeepEqual(want, src) {
		t.Fatalf("mutating a clone changed the source: %+v", src)
	}
}

func TestNewChatUsesUTC(t *testing.T) {
	c := New()
	if c.CreatedAt.Location() != time.UTC || c.UpdatedAt.Location() != time.UTC {
		t.Fatalf("chat times are not UTC: %v %v", c.CreatedAt, c.UpdatedAt)
	}
}
