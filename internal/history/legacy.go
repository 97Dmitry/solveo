package history

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
)

func legacyID(id, kind string, index int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", id, kind, index)))
	return hex.EncodeToString(h[:16])
}
func readLegacy(data []byte) (chat.Chat, error) {
	var old struct {
		ID        string    `json:"thread_id"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
		Options   struct{ Language, Model, Reasoning string }
		Messages  []struct{ Role, Text string }
		Images    []string
	}
	if e := json.Unmarshal(data, &old); e != nil {
		return chat.Chat{}, e
	}
	c := chat.Chat{Version: 2, ID: old.ID, ProviderID: "codex", Title: "Импортированный чат", CreatedAt: old.CreatedAt, UpdatedAt: old.UpdatedAt, ConfigRevision: 1, Config: chat.Config{Detail: "brief", CodeLanguage: old.Options.Language, ResponseLanguage: "Русский", Model: old.Options.Model, Reasoning: old.Options.Reasoning}, Messages: []chat.Message{}, Attachments: []chat.Attachment{}}
	c.ProviderState, _ = json.Marshal(struct {
		Version int    `json:"version"`
		ID      string `json:"session_id"`
	}{1, old.ID})
	for i, m := range old.Messages {
		c.Messages = append(c.Messages, chat.Message{ID: legacyID(c.ID, "message", i), Role: m.Role, Text: m.Text, Status: chat.StatusComplete, ConfigRevision: 1, AttachmentIDs: []string{}, Attempts: []chat.Attempt{}})
	}
	for i, p := range old.Images {
		c.Attachments = append(c.Attachments, chat.Attachment{ID: legacyID(c.ID, "image", i), Path: p})
	}
	return c, nil
}
