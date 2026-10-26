package app

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/97Dmitry/solveo/internal/chat"
)

// Diagnostics is the session log owner (internal/diagnostics.Session in production).
type Diagnostics interface {
	LogsDir() string
	// Archive packs recent sessions with summary and returns the archive path.
	Archive(summary string) (string, error)
	// PreviousCrash names the crash file of the previous session, or "".
	PreviousCrash() string
}

// DiagnosticsView is the renderer's view of the session log.
type DiagnosticsView struct {
	Available bool `json:"available"`
	// CrashNotice is shown once after a start that found a crash of the previous session.
	CrashNotice bool `json:"crash_notice"`
}

// RendererError is an uncaught renderer failure reported by a frontend.
type RendererError struct {
	Source  string `json:"source"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Stack   string `json:"stack"`
}

const (
	maxContentRunes       = 4096
	maxRendererMessage    = 2048
	maxRendererStack      = 6144
	rendererBurst         = 20
	rendererWindow        = 10 * time.Second
	maxRendererPerSession = 500
)

type diagnosticsState struct {
	mu          sync.Mutex
	crashNotice bool
	archiving   bool
	loggedErr   string
	windowStart time.Time
	inWindow    int
	reported    int
	suppressed  int
}

// content returns a text attribute only while the user allows content in the
// session log; otherwise the attribute is empty and handlers skip it.
func (c *Controller) content(key, text string) slog.Attr {
	if !c.logContent.Load() {
		return slog.Attr{}
	}
	return slog.String(key, truncateRunes(text, maxContentRunes))
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// logErrorChange records each new user-visible error once; publish calls it.
func (c *Controller) logErrorChange() {
	c.mu.Lock()
	current := c.err
	c.mu.Unlock()
	c.diag.mu.Lock()
	changed := current != c.diag.loggedErr
	c.diag.loggedErr = current
	c.diag.mu.Unlock()
	if changed && current != "" {
		c.logger.Warn("error shown", "error", current)
	}
}

// logTimingLine forwards content-free measurement lines from the CLI log.
func (c *Controller) logTimingLines(text string) {
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Codex timing: ") || strings.HasPrefix(line, "Solveo timing: ") {
			c.logger.Info("timing", "line", line)
		}
	}
}

// logTurn records how the last user turn of doc ended.
func (c *Controller) logTurn(opID string, started time.Time, doc chat.Chat, pending error) {
	if len(doc.Messages) == 0 {
		return
	}
	var turn chat.Message
	var answer string
	for i := len(doc.Messages) - 1; i >= 0; i-- {
		m := doc.Messages[i]
		if m.Role == chat.RoleUser {
			turn = m
			if i+1 < len(doc.Messages) {
				answer = doc.Messages[i+1].Text
			}
			break
		}
	}
	attrs := []any{"op", opID, "chat", doc.ID, "message", turn.ID, "status", turn.Status}
	if !started.IsZero() {
		attrs = append(attrs, "duration", time.Since(started).Round(time.Millisecond).String())
	}
	if n := len(turn.Attempts); n > 0 && turn.Attempts[n-1].Error != "" {
		attrs = append(attrs, "error", turn.Attempts[n-1].Error)
	}
	for _, a := range turn.Answers {
		attrs = append(attrs, slog.Group(string(a.Kind), "status", a.Status, "model", a.Model, "chars", utf8.RuneCountInString(a.Text)))
		if a.Error != "" {
			attrs = append(attrs, slog.String(string(a.Kind)+"_error", a.Error))
		}
		attrs = append(attrs, c.content(string(a.Kind)+"_text", a.Text))
	}
	if len(turn.Answers) == 0 {
		attrs = append(attrs, "answer_chars", utf8.RuneCountInString(answer), c.content("answer", answer))
	}
	if pending != nil {
		attrs = append(attrs, "save_error", pending.Error())
		c.logger.Error("request result not saved", attrs...)
		return
	}
	c.logger.Info("request finished", attrs...)
}

func (c *Controller) diagnosticsViewLocked() DiagnosticsView {
	c.diag.mu.Lock()
	defer c.diag.mu.Unlock()
	return DiagnosticsView{Available: c.deps.Diagnostics != nil, CrashNotice: c.diag.crashNotice}
}

// DismissCrashNotice hides the previous-crash notice for this session.
func (c *Controller) DismissCrashNotice() {
	c.diag.mu.Lock()
	c.diag.crashNotice = false
	c.diag.mu.Unlock()
	c.mu.Lock()
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
}

func (c *Controller) OpenLogsFolder() error {
	if c.deps.Diagnostics == nil {
		return errors.New("журнал диагностики недоступен")
	}
	if c.deps.NativeHost == nil {
		return errors.New("открытие папок недоступно")
	}
	return c.deps.NativeHost.OpenFolder(c.deps.Diagnostics.LogsDir())
}

// CreateDiagnosticsArchive packs recent session logs with a content-free
// summary and opens the folder holding the archive. It needs no operation slot.
func (c *Controller) CreateDiagnosticsArchive() (string, error) {
	if c.deps.Diagnostics == nil {
		return "", errors.New("журнал диагностики недоступен")
	}
	c.diag.mu.Lock()
	if c.diag.archiving {
		c.diag.mu.Unlock()
		return "", errors.New("архив диагностики уже собирается")
	}
	c.diag.archiving = true
	c.diag.crashNotice = false
	c.diag.mu.Unlock()
	defer func() {
		c.diag.mu.Lock()
		c.diag.archiving = false
		c.diag.mu.Unlock()
	}()
	c.logger.Info("diagnostics archive requested")
	path, err := c.deps.Diagnostics.Archive(c.diagnosticsSummary())
	if err != nil {
		c.logger.Error("diagnostics archive failed", "error", err)
		return "", fmt.Errorf("не удалось собрать архив диагностики: %w", err)
	}
	c.mu.Lock()
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	if c.deps.NativeHost != nil {
		if err := c.deps.NativeHost.OpenFolder(filepath.Dir(path)); err != nil {
			return path, err
		}
	}
	return path, nil
}

// diagnosticsSummary lists settings that explain behavior, never their text.
func (c *Controller) diagnosticsSummary() string {
	c.mu.Lock()
	cfg := c.settings
	chats := len(c.summaries)
	op := c.op.Operation
	pending := c.pending != nil
	c.mu.Unlock()
	audio := c.AudioSnapshot()
	p := c.deps.Provider.Describe()
	var b strings.Builder
	fmt.Fprintf(&b, "created_at=%s\n", c.deps.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "llm_provider=%s\n", p.ID)
	fmt.Fprintf(&b, "chats=%d\n", chats)
	fmt.Fprintf(&b, "operation=%s/%s pending_save=%t\n", op.Kind, op.Phase, pending)
	fmt.Fprintf(&b, "theme=%s ui_scale=%d font_size=%d always_on_top=%t\n", cfg.Theme, cfg.UIScale, cfg.FontSize, cfg.AlwaysOnTop)
	fmt.Fprintf(&b, "global_instructions_chars=%d\n", utf8.RuneCountInString(cfg.GlobalInstructions))
	fmt.Fprintf(&b, "stt_provider=%s stt_model=%s dictionary_terms=%d builder_model=%s builder_reasoning=%s\n", audio.Provider.ID, audio.Model, len(cfg.Audio.ContextPhrases), cfg.Audio.BuilderModel, cfg.Audio.BuilderReasoning)
	fmt.Fprintf(&b, "listening=%t\n", audio.Listening)
	fmt.Fprintf(&b, "log_content=%t\n", cfg.Diagnostics.IncludeContent)
	for action, key := range cfg.Hotkeys {
		fmt.Fprintf(&b, "hotkey.%s=%s\n", action, key)
	}
	return b.String()
}

// ReportRendererError records an uncaught renderer failure. Reports are
// bounded in size and rate; excess reports are counted, not stored.
func (c *Controller) ReportRendererError(in RendererError) error {
	switch in.Source {
	case "main", "audio":
	default:
		return errors.New("неизвестный источник диагностики")
	}
	switch in.Kind {
	case "error", "rejection", "render":
	default:
		return errors.New("неизвестный вид диагностики")
	}
	now := time.Now()
	c.diag.mu.Lock()
	if now.Sub(c.diag.windowStart) >= rendererWindow {
		c.diag.windowStart = now
		c.diag.inWindow = 0
	}
	if c.diag.inWindow >= rendererBurst || c.diag.reported >= maxRendererPerSession {
		c.diag.suppressed++
		c.diag.mu.Unlock()
		return nil
	}
	c.diag.inWindow++
	c.diag.reported++
	suppressed := c.diag.suppressed
	c.diag.suppressed = 0
	c.diag.mu.Unlock()
	attrs := []any{"source", in.Source, "kind", in.Kind, "message", truncateRunes(strings.ToValidUTF8(in.Message, "�"), maxRendererMessage), "stack", truncateRunes(strings.ToValidUTF8(in.Stack, "�"), maxRendererStack)}
	if suppressed > 0 {
		attrs = append(attrs, "suppressed_before", suppressed)
	}
	c.logger.Error("renderer error", attrs...)
	return nil
}
