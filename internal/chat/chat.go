// Package chat defines provider-independent chat documents.
package chat

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

// DefaultTitle marks a chat whose title is still generated from its first question.
const DefaultTitle = "Новый чат"

// Message roles.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message and attempt statuses persisted in the archive.
const (
	StatusRequesting  = "requesting"
	StatusComplete    = "complete"
	StatusFailed      = "failed"
	StatusCanceled    = "canceled"
	StatusUnknown     = "unknown"
	StatusInterrupted = "interrupted"
)

type Config struct {
	Context          string `json:"context"`
	Instructions     string `json:"instructions"`
	ResponseLanguage string `json:"response_language"`
	CodeLanguage     string `json:"code_language"`
	Detail           string `json:"detail"`
	Model            string `json:"model"`
	Reasoning        string `json:"reasoning"`
}
type Attempt struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	ConfigRevision uint64 `json:"config_revision"`
	Error          string `json:"error"`
}
type Message struct {
	ID             string    `json:"id"`
	Role           string    `json:"role"`
	Text           string    `json:"text"`
	Status         string    `json:"status"`
	AttachmentIDs  []string  `json:"attachment_ids"`
	ConfigRevision uint64    `json:"config_revision"`
	Attempts       []Attempt `json:"attempts"`
}
type Attachment struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}
type Chat struct {
	Version        int             `json:"version"`
	ID             string          `json:"id"`
	ProviderID     string          `json:"provider_id"`
	Title          string          `json:"title"`
	ProviderState  json.RawMessage `json:"provider_state,omitempty"`
	Config         Config          `json:"config"`
	ConfigRevision uint64          `json:"config_revision"`
	Messages       []Message       `json:"messages"`
	Attachments    []Attachment    `json:"attachments"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// Now is the application clock. All persisted and displayed times are UTC.
func Now() time.Time { return time.Now().UTC() }

func ID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func New() Chat {
	now := Now()
	return Chat{
		Version:        2,
		ID:             ID(),
		ProviderID:     "codex",
		Title:          DefaultTitle,
		ConfigRevision: 1,
		Config:         Config{ResponseLanguage: "Русский", CodeLanguage: "auto", Detail: "brief"},
		Messages:       []Message{},
		Attachments:    []Attachment{},
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

// Clone prevents snapshots/workers from sharing mutable slices with the controller.
func Clone(c Chat) Chat {
	b, e := json.Marshal(c)
	if e != nil {
		panic(e)
	}
	var out Chat
	if e = json.Unmarshal(b, &out); e != nil {
		panic(e)
	}
	return out
}
