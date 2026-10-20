// Package app owns chat state and serializes all user operations.
package app

import (
	"context"
	"errors"
	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/platform"
	"github.com/97Dmitry/solveo/internal/settings"
	"image"
	"time"
)

var (
	ErrBusy        = errors.New("another operation is running")
	ErrQueueFull   = errors.New("screenshot queue is full (20)")
	ErrStale       = errors.New("state changed; refresh and try again")
	ErrEmpty       = errors.New("enter a question or attach a screenshot")
	ErrSavePending = errors.New("save the received result or explicitly discard it first")
)

type Archive interface {
	Save(history.SaveInput) (chat.Chat, error)
	Load(string) (chat.Chat, error)
	List() ([]history.Summary, error)
	ImagePath(string, string) (string, error)
	Delete(string) error
}
type SettingsStore interface {
	Load() (settings.Config, error)
	Save(settings.Config) error
}
type Capturer interface {
	SavePrimary(string) error
	SaveRegion(string, image.Rectangle) error
}
type NativeHost interface {
	BeginSelection(string) error
	ShowLog() error
	ShowImages([]platform.ViewImage, int) error
	CloseImages() error
	RebindImages(map[string]string) error
	SetTheme(bool) error
	SetLog(string) error
}
type Window interface {
	SetAlwaysOnTop(bool)
	Show()
	Hide()
	Toggle()
}
type Dependencies struct {
	Store         Archive
	SettingsStore SettingsStore
	Provider      llm.Provider
	Capturer      Capturer
	NativeHost    NativeHost
	Window        Window
	TempDir       string
	Emit          func(string, interface{})
	Now           func() time.Time
}
type Draft struct {
	Text     string `json:"text"`
	Revision uint64 `json:"revision"`
}
type DraftInput struct {
	ChatID   string `json:"chat_id"`
	Text     string `json:"text"`
	Revision uint64 `json:"revision"`
}
type ConfigInput struct {
	ChatID           string      `json:"chat_id"`
	Title            string      `json:"title"`
	Config           chat.Config `json:"config"`
	ExpectedRevision uint64      `json:"expected_revision"`
}
type SubmitInput struct {
	RequestID      string   `json:"request_id"`
	IntentID       string   `json:"intent_id"`
	ChatID         string   `json:"chat_id"`
	Text           string   `json:"text"`
	DraftRevision  uint64   `json:"draft_revision"`
	AttachmentIDs  []string `json:"attachment_ids"`
	RetryMessageID string   `json:"retry_message_id"`
}
type SubmitIntent struct {
	ID            string `json:"id"`
	ChatID        string `json:"chat_id"`
	DraftRevision uint64 `json:"draft_revision"`
	ExpiresAt     int64  `json:"expires_at"`
}
type Operation struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Phase   string `json:"phase"`
	Message string `json:"message"`
}
type Controls struct {
	CanSubmit     bool `json:"can_submit"`
	CanCapture    bool `json:"can_capture"`
	CanEditConfig bool `json:"can_edit_config"`
	CanCancel     bool `json:"can_cancel"`
	CanSwitch     bool `json:"can_switch"`
	CanRetry      bool `json:"can_retry"`
	CanDiscard    bool `json:"can_discard"`
}
type AttachmentView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	URL   string `json:"url"`
}
type HistorySummary struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	ProviderID string `json:"provider_id"`
	UpdatedAt  string `json:"updated_at"`
	Prepared   bool   `json:"prepared"`
}
type Snapshot struct {
	Revision       uint64           `json:"revision"`
	ActiveChatID   string           `json:"active_chat_id"`
	Title          string           `json:"title"`
	ProviderID     string           `json:"provider_id"`
	Config         chat.Config      `json:"config"`
	ConfigRevision uint64           `json:"config_revision"`
	Messages       []chat.Message   `json:"messages"`
	Queue          []AttachmentView `json:"queue"`
	Images         []AttachmentView `json:"images"`
	Draft          Draft            `json:"draft"`
	Operation      Operation        `json:"operation"`
	Controls       Controls         `json:"controls"`
	Settings       settings.Config  `json:"settings"`
	Provider       llm.Descriptor   `json:"provider"`
	History        []HistorySummary `json:"history"`
	Error          string           `json:"error"`
	Log            string           `json:"log"`
	Uncertain      bool             `json:"uncertain"`
}
type queued struct{ ID, Path, Label string }
type pendingSave struct {
	doc        chat.Chat
	sources    []history.Source
	cleanupIDs []string
	draft      Draft
	clearDraft bool
}
type operation struct {
	Operation
	chatID string
	cancel context.CancelFunc
}
