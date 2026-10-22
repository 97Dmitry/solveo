package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/profiles"
	"github.com/97Dmitry/solveo/internal/settings"
	"github.com/97Dmitry/solveo/internal/workstate"
)

type Controller struct {
	profiles          []profiles.Profile
	profilesErr       string
	settingsMu        sync.Mutex
	mu                sync.Mutex
	deps              Dependencies
	ctx               context.Context
	cancel            context.CancelFunc
	workers           sync.WaitGroup
	preparationCancel context.CancelFunc
	searchCancel      context.CancelFunc
	// Native hotkey state; changes are serialized by settingsMu (hotkeys.go).
	hotkeyErrors     map[string]string
	hotkeysSuspended bool
	resumeTimer      *time.Timer
	revision         uint64
	active           chat.Chat
	settings         settings.Config
	summaries        []history.Summary
	drafts           map[string]Draft
	queues           map[string][]queued
	op               operation
	pending          *pendingSave
	requests         map[string]bool
	intent           *SubmitIntent
	err              string
	logs             string
	logFlushPending  bool
	closed           bool
	selectionMode    string

	streams            map[streamKey]StreamBlock
	streamOpen         map[streamKey]bool
	streamRevision     uint64
	streamFlushPending bool
	login              LoginView

	// Unfinished work persisted in Dependencies.Work (work.go).
	submitted     map[string]workstate.Submitted
	checkpoints   map[streamKey]workstate.Block
	recovered     []workstate.Block
	statePersist  *persister
	streamPersist *persister
	stateErr      string
	streamErr     string
}

var errNoConnection = errors.New("подключение LLM не поддерживает вход")

func New(d Dependencies) (*Controller, error) {
	if d.Store == nil || d.SettingsStore == nil || d.Provider == nil || d.Work == nil {
		return nil, errors.New("не заданы зависимости приложения")
	}
	if d.Now == nil {
		d.Now = chat.Now
	}
	cfg, e := d.SettingsStore.Load()
	if e != nil {
		return nil, e
	}
	list, e := d.Store.List()
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Controller{
		deps:      d,
		ctx:       ctx,
		cancel:    cancel,
		revision:  1,
		settings:  cfg,
		summaries: list,
		drafts:    map[string]Draft{},
		queues:    map[string][]queued{},
		requests:  map[string]bool{},
		op:        operation{Operation: Operation{Phase: phaseIdle}},

		streams:     map[streamKey]StreamBlock{},
		streamOpen:  map[streamKey]bool{},
		submitted:   map[string]workstate.Submitted{},
		checkpoints: map[streamKey]workstate.Block{},
	}
	c.loadProfiles()
	c.initPersisters()
	c.err = joinNotices(c.restoreWork())
	doc := newChat(cfg.GlobalInstructions)
	if len(c.summaries) > 0 {
		doc, e = d.Store.Load(c.summaries[0].ID)
	} else {
		doc, e = d.Store.Save(history.SaveInput{Chat: doc})
	}
	if e != nil {
		c.statePersist.stop()
		c.streamPersist.stop()
		return nil, e
	}
	c.active = doc
	c.rememberLocked(doc)
	c.statePersist.resume()
	c.streamPersist.resume()
	c.prepareConnection()
	// A shortcut taken by another application must not prevent startup;
	// Snapshot.HotkeyErrors shows it in the settings instead.
	c.hotkeyErrors, _ = c.registerHotkeys(cfg.Hotkeys)
	c.mu.Lock()
	c.prepareChatLocked()
	c.mu.Unlock()
	return c, nil
}
func (c *Controller) rememberLocked(doc chat.Chat) {
	s := history.Summary{ID: doc.ID, Title: doc.Title, ProviderID: doc.ProviderID, UpdatedAt: doc.UpdatedAt, Prepared: len(doc.Messages) == 0}
	for i, v := range c.summaries {
		if v.ID == doc.ID {
			s.Pinned = v.Pinned
			c.summaries = append(c.summaries[:i], c.summaries[i+1:]...)
			break
		}
	}
	c.summaries = append([]history.Summary{s}, c.summaries...)
}
func (c *Controller) availableLocked() error {
	if c.closed {
		return errors.New("приложение завершается")
	}
	if c.pending != nil {
		return ErrSavePending
	}
	if c.op.Phase != phaseIdle {
		return ErrBusy
	}
	return nil
}
func (c *Controller) changedLocked() { c.revision++ }
func (c *Controller) publish() {
	if c.deps.Emit != nil {
		c.deps.Emit("solveo:state", c.Snapshot())
	}
}
func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	doc := chat.Clone(c.active)
	q := []AttachmentView{}
	imgs := []AttachmentView{}
	makeView := func(id, label string) AttachmentView {
		return AttachmentView{ID: id, Label: label, URL: "/attachments/" + url.PathEscape(doc.ID) + "/" + url.PathEscape(id)}
	}
	for _, v := range c.queues[doc.ID] {
		q = append(q, makeView(v.ID, v.Label))
	}
	for i, a := range doc.Attachments {
		imgs = append(imgs, makeView(a.ID, fmt.Sprintf("Снимок %d", i+1)))
	}
	idle := c.op.Phase == phaseIdle && !c.closed
	available := idle && c.pending == nil
	providerOK := doc.ProviderID == c.deps.Provider.Describe().ID
	retry := false
	uncertain := false
	for _, m := range doc.Messages {
		if m.Role == chat.RoleUser && m.Status != chat.StatusComplete {
			retry = true
			if m.Status == chat.StatusUnknown || m.Status == chat.StatusInterrupted {
				uncertain = true
			}
		}
	}
	messages := doc.Messages
	if messages == nil {
		messages = []chat.Message{}
	}
	for i := range messages {
		if messages[i].AttachmentIDs == nil {
			messages[i].AttachmentIDs = []string{}
		}
		if messages[i].Attempts == nil {
			messages[i].Attempts = []chat.Attempt{}
		}
	}
	summaries := []HistorySummary{}
	for _, pinned := range []bool{true, false} {
		for _, h := range c.summaries {
			if h.Pinned == pinned {
				summaries = append(summaries, HistorySummary{ID: h.ID, Title: h.Title, ProviderID: h.ProviderID, UpdatedAt: h.UpdatedAt.UTC().Format(time.RFC3339Nano), Prepared: h.Prepared, Pinned: h.Pinned})
			}
		}
	}
	workErr := c.stateErr
	if workErr == "" {
		workErr = c.streamErr
	}
	return Snapshot{
		Profiles:       c.profileViewsLocked(),
		ProfilesError:  c.profilesErr,
		Revision:       c.revision,
		ActiveChatID:   doc.ID,
		Title:          doc.Title,
		ProviderID:     doc.ProviderID,
		Config:         doc.Config,
		ConfigRevision: doc.ConfigRevision,
		Messages:       messages,
		Queue:          q,
		Images:         imgs,
		Draft:          c.drafts[doc.ID],
		Operation:      c.op.Operation,
		Controls: Controls{
			CanSubmit:     available && providerOK,
			CanCapture:    available && providerOK && len(q) < 20,
			CanEditConfig: available && providerOK,
			CanCancel:     c.op.Phase == phaseRequesting,
			CanSwitch:     available,
			CanRetry:      available && providerOK && retry,
			CanDiscard:    c.pending != nil,
		},
		Settings: c.settings,

		HotkeyErrors: c.hotkeyErrorsLocked(),
		Provider:     c.deps.Provider.Describe(),
		History:      summaries,
		Error:        c.err,
		Uncertain:    uncertain,
		Login:        c.login,
		WorkError:    workErr,
		Recovered:    c.recoveredLocked(doc),
	}
}
func (c *Controller) SetDraft(in DraftInput) error {
	if len(in.Text) > 1<<20 {
		return errors.New("черновик длиннее 1 МиБ")
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrBusy
	}
	if in.ChatID != c.active.ID {
		c.mu.Unlock()
		return ErrStale
	}
	if !chat.ValidReference(c.active.Messages, in.ReplyToID, in.ReplyToKind) {
		c.mu.Unlock()
		return errors.New("выбранный ответ недоступен для уточнения")
	}
	old := c.drafts[in.ChatID]
	if in.Revision >= old.Revision {
		c.drafts[in.ChatID] = Draft{Text: in.Text, Revision: in.Revision, ReplyToID: in.ReplyToID, ReplyToKind: in.ReplyToKind}
		c.changedLocked()
		c.statePersist.mark()
	}
	c.mu.Unlock()
	c.publish()
	return nil
}
func validateConfig(cfg chat.Config) error { return chat.ValidateConfig(cfg) }
func (c *Controller) SaveChatConfig(in ConfigInput) (Snapshot, error) {
	if e := validateConfig(in.Config); e != nil {
		return Snapshot{}, e
	}
	c.mu.Lock()
	if e := c.availableLocked(); e != nil {
		c.mu.Unlock()
		return Snapshot{}, e
	}
	if in.ChatID != c.active.ID || in.ExpectedRevision != c.active.ConfigRevision {
		c.mu.Unlock()
		return Snapshot{}, ErrStale
	}
	doc := chat.Clone(c.active)
	doc.Config = in.Config
	doc.ConfigRevision++
	doc.Title = strings.TrimSpace(in.Title)
	if doc.Title == "" {
		doc.Title = chat.DefaultTitle
	}
	if c.preparationCancel != nil {
		c.preparationCancel()
	}
	c.op = operation{Operation: Operation{ID: chat.ID(), Kind: kindConfig, Phase: phaseSaving}}
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	saved, e := c.deps.Store.Save(history.SaveInput{Chat: doc})
	c.mu.Lock()
	c.op = operation{Operation: Operation{Phase: phaseIdle}}
	if e == nil {
		c.active = chat.Clone(saved)
		c.rememberLocked(saved)
	} else {
		c.err = e.Error()
	}
	c.prepareChatLocked()
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	return c.Snapshot(), e
}
func (c *Controller) NewChat() (Snapshot, error)           { return c.switchChat("", "", 0) }
func (c *Controller) OpenChat(id string) (Snapshot, error) { return c.switchChat(id, "", 0) }
func (c *Controller) switchChat(id, profileID string, profileRevision uint64) (Snapshot, error) {
	c.mu.Lock()
	if e := c.availableLocked(); e != nil {
		c.mu.Unlock()
		return Snapshot{}, e
	}
	var profile profiles.Profile
	if profileID != "" {
		var err error
		profile, err = c.profileLocked(profileID, profileRevision)
		if err != nil {
			c.mu.Unlock()
			return Snapshot{}, err
		}
	}
	if c.preparationCancel != nil {
		c.preparationCancel()
	}
	c.op = operation{Operation: Operation{ID: chat.ID(), Kind: kindOpen, Phase: phaseSaving}}
	c.intent = nil
	globalInstructions := c.settings.GlobalInstructions
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	var doc chat.Chat
	var e error
	if id == "" {
		doc = newChat(globalInstructions)
		if profileID != "" {
			doc.Config = profile.Config
			doc.Title = profile.Name
		}
		doc, e = c.deps.Store.Save(history.SaveInput{Chat: doc})
	} else {
		doc, e = c.deps.Store.Load(id)
	}
	c.mu.Lock()
	if e == nil {
		c.active = chat.Clone(doc)
		c.rememberLocked(doc)
		c.err = ""
	} else {
		c.err = e.Error()
	}
	c.op = operation{Operation: Operation{Phase: phaseIdle}}
	c.prepareChatLocked()
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	return c.Snapshot(), e
}
func (c *Controller) SetAlwaysOnTop(value bool) (Snapshot, error) {
	// Read-modify-write under settingsMu so a concurrent UpdateSettings is not lost.
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	c.mu.Lock()
	cfg := c.settings
	c.mu.Unlock()
	cfg.AlwaysOnTop = value
	return c.updateSettingsLocked(cfg)
}
func (c *Controller) UpdateSettings(cfg settings.Config) (Snapshot, error) {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	return c.updateSettingsLocked(cfg)
}
func (c *Controller) updateSettingsLocked(cfg settings.Config) (Snapshot, error) {
	cfg, e := settings.Normalize(cfg)
	if e != nil {
		return Snapshot{}, e
	}
	if e = settings.Validate(cfg); e != nil {
		return Snapshot{}, e
	}
	c.mu.Lock()
	old := c.settings
	c.mu.Unlock()
	hotkeysChanged := !maps.Equal(old.Hotkeys, cfg.Hotkeys)
	if hotkeysChanged {
		if e = c.applyHotkeysLocked(old, cfg); e != nil {
			return Snapshot{}, e
		}
	}
	if e = c.deps.SettingsStore.Save(cfg); e != nil {
		if hotkeysChanged {
			restored, _ := c.registerHotkeys(old.Hotkeys)
			c.setHotkeyState(restored, false)
			c.publish()
		}
		return Snapshot{}, e
	}
	c.mu.Lock()
	c.settings = cfg
	c.changedLocked()
	c.mu.Unlock()
	if c.deps.Window != nil {
		c.deps.Window.SetAlwaysOnTop(cfg.AlwaysOnTop)
	}
	if c.deps.NativeHost != nil {
		_ = c.deps.NativeHost.SetTheme(cfg.Theme == "dark")
	}
	c.publish()
	return c.Snapshot(), nil
}
func (c *Controller) ReportError(e error) {
	if e == nil {
		return
	}
	c.mu.Lock()
	c.err = e.Error()
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
}
func (c *Controller) Close(ctx context.Context) error {
	c.mu.Lock()
	if c.op.Phase != phaseIdle && c.op.Kind != kindSubmit && c.op.Kind != kindCapture && c.op.Kind != kindCheck {
		c.mu.Unlock()
		return ErrBusy
	}
	if c.pending != nil {
		c.mu.Unlock()
		return ErrSavePending
	}
	c.closed = true
	c.cancel()
	c.intent = nil
	if c.resumeTimer != nil {
		c.resumeTimer.Stop()
	}
	c.mu.Unlock()
	done := make(chan struct{})
	go func() { c.workers.Wait(); close(done) }()
	select {
	case <-ctx.Done():
		c.mu.Lock()
		c.closed = false
		c.ctx, c.cancel = context.WithCancel(context.Background())
		c.prepareChatLocked()
		c.mu.Unlock()
		return ctx.Err()
	case <-done:
	}
	c.mu.Lock()
	if c.pending != nil {
		c.closed = false
		c.ctx, c.cancel = context.WithCancel(context.Background())
		c.prepareChatLocked()
		c.mu.Unlock()
		return ErrSavePending
	}
	c.mu.Unlock()
	// Unfinished work stays on disk for the next start; write its final state.
	if err := errors.Join(c.statePersist.flush(), c.streamPersist.flush()); err != nil {
		// Keep persisters and the provider usable so disk recovery and another
		// explicit exit can save the data instead of silently discarding it.
		c.mu.Lock()
		c.closed = false
		c.ctx, c.cancel = context.WithCancel(context.Background())
		c.prepareChatLocked()
		c.changedLocked()
		c.mu.Unlock()
		c.publish()
		return fmt.Errorf("незавершённая работа не сохранена; восстановите запись и повторите выход: %w", err)
	}
	c.statePersist.stop()
	c.streamPersist.stop()
	// Workers are drained, so no request uses the streaming connection any more.
	if conn, ok := c.deps.Provider.(llm.Connection); ok {
		if err := conn.Close(ctx); err != nil {
			c.appendLog(fmt.Sprintf("Codex app-server: %v\n", err))
		}
	}
	return nil
}

// newChat snapshots application settings without coupling the domain to settings.
func newChat(instructions string) chat.Chat {
	doc := chat.New()
	doc.GlobalInstructions = instructions
	return doc
}
