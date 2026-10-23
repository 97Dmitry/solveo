package chat

import (
	"encoding/json"
	"testing"
)

func TestTranscriptOriginSurvivesArchiveAndClone(t *testing.T) {
	d := New()
	d.Messages = append(d.Messages, Message{Role: RoleUser, Text: "горутинки", FromTranscript: true})
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var loaded Chat
	if err := json.Unmarshal(b, &loaded); err != nil {
		t.Fatal(err)
	}
	if !Clone(loaded).Messages[0].FromTranscript {
		t.Fatal("lost transcript metadata")
	}
	var legacy Message
	if err := json.Unmarshal([]byte(`{"role":"user","text":"manual"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.FromTranscript {
		t.Fatal("legacy text marked as transcript")
	}
}
