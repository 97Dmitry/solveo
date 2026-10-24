// Package app owns chat state and serializes all user operations.
package app

import (
	"context"
	"errors"
	"image"
	"log/slog"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/hotkeys"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/platform"
	"github.com/97Dmitry/solveo/internal/profiles"
	"github.com/97Dmitry/solveo/internal/settings"
	"github.com/97Dmitry/solveo/internal/workstate"
)

var (
	ErrBusy        = errors.New("выполняется другая операция")
	ErrQueueFull   = errors.New("очередь снимков заполнена (20)")
	ErrStale       = errors.New("состояние изменилось, повторите действие")
	ErrEmpty       = errors.New("введите вопрос или добавьте снимок")
	ErrSavePending = errors.New("сначала сохраните полученный ответ или явно отбросьте его")
)

// Operation phases exposed to the frontend.
const (
	phaseIdle        = "idle"
	phaseSelecting   = "selecting"
	phaseCapturing   = "capturing"
	phaseSaving      = "saving"
	phaseRequesting  = "requesting"
	phaseSavePending = "save-pending"
)

// Operation kinds.
const (
	kindSubmit  = "submit"
	kindCapture = "capture"
	kindCheck   = "check"
	kindConfig  = "config"
	kindOpen    = "open"
	kindDelete  = "delete"
	kindDiscard = "discard"
	kindExport  = "export"
)

type Archive interface {
	Save(history.SaveInput) (chat.Chat, error)
	Load(string) (chat.Chat, error)
	List() ([]history.Summary, error)
	ImagePath(string, string) (string, error)
	AttachmentPath(string, chat.Attachment) (string, error)
	Delete(string) error
	Exists(string) (bool, error)
	SetPinned(string, bool) error
	Search(context.Context, string, int) ([]history.SearchHit, error)
	Export(string, time.Time) (history.ExportResult, error)
	ExportPath(string) (string, error)
}

// WorkStore persists unfinished work: drafts, queued PNGs and answer checkpoints.
type WorkStore interface {
	Load() (workstate.State, string, error)
	Save(workstate.State) error
	LoadStream() ([]workstate.Block, error)
	SaveStream([]workstate.Block) error
	CapturePath(string) (string, error)
	Commit(string) (string, error)
	WriteImage(string, []byte) (string, error)
	Path(string) (string, error)
	Has(string) (bool, error)
	Prune(map[string]bool) error
}
type ProfilesStore interface {
	Load() ([]profiles.Profile, error)
	Save([]profiles.Profile) error
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
	OpenFolder(string) error
	SetHotkeys([]hotkeys.Binding) (map[string]error, error)
}
type Window interface {
	SetAlwaysOnTop(bool)
	Show()
	Hide()
	Toggle()
}
type Dependencies struct {
	ProfilesStore ProfilesStore
	Store         Archive
	SettingsStore SettingsStore
	Provider      llm.Provider
	Capturer      Capturer
	NativeHost    NativeHost
	Window        Window
	Work          WorkStore
	Emit          func(string, interface{})
	Now           func() time.Time
	Audio         AudioDeps
	// Log is the session log; nil discards records.
	Log         *slog.Logger
	Diagnostics Diagnostics
}
type Draft struct {
	ReplyToID   string `json:"reply_to_id,omitempty"`
	ReplyToKind string `json:"reply_to_kind,omitempty"`
	Text        string `json:"text"`
	Revision    uint64 `json:"revision"`
}
type DraftInput struct {
	ReplyToID   string `json:"reply_to_id,omitempty"`
	ReplyToKind string `json:"reply_to_kind,omitempty"`
	ChatID      string `json:"chat_id"`
	Text        string `json:"text"`
	Revision    uint64 `json:"revision"`
}
type ProfileView struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Config    chat.Config `json:"config"`
	Revision  uint64      `json:"revision"`
	CreatedAt string      `json:"created_at"`
	UpdatedAt string      `json:"updated_at"`
}
type ProfileInput struct {
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	Config           chat.Config `json:"config"`
	ExpectedRevision uint64      `json:"expected_revision"`
}
type ConfigInput struct {
	ChatID           string      `json:"chat_id"`
	Title            string      `json:"title"`
	Config           chat.Config `json:"config"`
	ExpectedRevision uint64      `json:"expected_revision"`
}
type SubmitInput struct {
	ReplyToID      string   `json:"reply_to_id,omitempty"`
	ReplyToKind    string   `json:"reply_to_kind,omitempty"`
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
	Pinned     bool   `json:"pinned"`
}
type DeleteFailure struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Error string `json:"error"`
}
type DeleteResult struct {
	Deleted []string        `json:"deleted"`
	Failed  []DeleteFailure `json:"failed"`
}
type SearchHit struct {
	ChatID    string `json:"chat_id"`
	Title     string `json:"title"`
	Snippet   string `json:"snippet"`
	Matches   int    `json:"matches"`
	Pinned    bool   `json:"pinned"`
	UpdatedAt string `json:"updated_at"`
}
type Snapshot struct {
	Profiles       []ProfileView    `json:"profiles"`
	ProfilesError  string           `json:"profiles_error"`
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
	Uncertain      bool             `json:"uncertain"`
	Login          LoginView        `json:"login"`
	// WorkError is set while drafts, the queue or answer checkpoints cannot be written.
	WorkError string `json:"work_error"`
	// Recovered is text received before a crash for interrupted attempts of this chat.
	Recovered []StreamBlock `json:"recovered"`
	// HotkeyErrors names actions whose current shortcut is not registered.
	HotkeyErrors map[string]string `json:"hotkey_errors"`
	Diagnostics  DiagnosticsView   `json:"diagnostics"`
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
	chatID  string
	cancel  context.CancelFunc
	started time.Time
}
