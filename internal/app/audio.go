package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/97Dmitry/solveo/internal/audio"
	"github.com/97Dmitry/solveo/internal/audiohistory"
	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/questionbuilder"
	"github.com/97Dmitry/solveo/internal/secrets"
	"github.com/97Dmitry/solveo/internal/stt"
)

// Local speech recognition: readiness checks, model and listening lifecycle, the in-memory transcript and
// sending selected phrases. Its state has its own mutex; when both are needed, audio.mu is released before
// c.mu is taken (SubmitTranscript), never the other way round.

const (
	maxTranscriptSegments = 300
	audioEmitDelay        = 200 * time.Millisecond
	audioFlushTimeout     = 10 * time.Second
	audioStopTimeout      = 15 * time.Second
	maxProgressRunes      = 300
	// UI texts stay short (the renderer rejects oversized events); full texts go to the native log.
	maxUITextRunes = 2000
	gapMessage     = "Распознавание не успевает, часть звука пропущена"
)

// AudioDeps is optional: without providers speech recognition is unavailable. Every provider is reached
// only through stt.Provider; the first one is the default.
type AudioDeps struct {
	History   AudioHistoryStore
	Providers []stt.Provider
	Capturer  audio.Capturer
	// Secrets keeps provider API keys; required when a provider NeedsKey.
	Secrets secrets.Store
}

const maxSTTKeyBytes = 512

// AudioProviderOption lists a provider the user can choose.
type AudioProviderOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Local bool   `json:"local"`
}

type AudioSourceView struct {
	Track  string `json:"track"`
	Device string `json:"device"`
	Active bool   `json:"active"`
	Error  string `json:"error"`
}

// TranscriptSegment is one utterance; its ID is the worker's utterance ID.
type TranscriptSegment struct {
	ProviderLabel string `json:"provider_label,omitempty"`
	ID            string `json:"id"`
	Track         string `json:"track"`
	StartedAt     string `json:"started_at"`
	Text          string `json:"text"`
	Final         bool   `json:"final"`
	// Sent marks a phrase included in an accepted submission (selection, question card or «new phrases»).
	Sent bool `json:"sent,omitempty"`
}

type AudioView struct {
	HistoryEnabled bool                  `json:"history_enabled"`
	HistoryError   string                `json:"history_error"`
	Available      bool                  `json:"available"`
	Provider       stt.Description       `json:"provider"`
	Providers      []AudioProviderOption `json:"providers"`
	Revision       uint64                `json:"revision"`
	Checks         []stt.Check           `json:"checks"`
	Busy           string                `json:"busy"`
	Progress       string                `json:"progress"`
	Model          string                `json:"model"`
	Listening      bool                  `json:"listening"`
	Sources        []AudioSourceView     `json:"sources"`
	Segments       []TranscriptSegment   `json:"segments"`
	Error          string                `json:"error"`
	// KeyConfigured tells whether the active provider's key is stored; the key itself never leaves Go.
	KeyConfigured bool `json:"key_configured"`
	// SendingUnsent: «send new phrases» waits for a phrase that was still being recognized;
	// SendError is its last failure, cleared by the next attempt.
	SendingUnsent bool   `json:"sending_unsent"`
	SendError     string `json:"send_error"`
}

type SubmitTranscriptInput struct {
	ChatID     string   `json:"chat_id"`
	RequestID  string   `json:"request_id"`
	SegmentIDs []string `json:"segment_ids"`
}

// Busy and model states published in AudioView.
const (
	audioBusyChecking  = "checking"
	audioBusyPreparing = "preparing"
	audioBusyStarting  = "starting"
	audioBusyStopping  = "stopping"
	audioBusyOpening   = "opening"
	audioBusySwitching = "switching"
	audioModelStopped  = "stopped"
	audioModelStarting = "starting"
	audioModelReady    = "ready"
	audioModelFailed   = "failed"
)

type audioSource struct {
	view   AudioSourceView
	stream string
	stop   func()
}

type audioState struct {
	history  audioHistoryState
	mu       sync.Mutex
	revision uint64
	checks   []stt.Check
	busy     string
	progress string
	model    string
	err      string
	gap      bool
	session  stt.SessionAPI
	// provider started the current session; Stop always goes to it.
	provider  stt.Provider
	listening bool
	sources   []*audioSource
	tracks    map[string]string // stream ID → track, kept after stop for late finals
	segments  []TranscriptSegment
	revisions map[string]int
	emitTimer *time.Timer
	// changed is closed by the next audioChangedLocked (see changeLocked).
	changed       chan struct{}
	sendingUnsent bool
	sendError     string
	keys          map[string]bool // provider ID → key stored
	// providerID is the selected provider (settings.Audio.Provider), mirrored here so views never take c.mu.
	providerID string
}

func (a *audioState) init() {
	a.model = audioModelStopped
	a.tracks = map[string]string{}
	a.revisions = map[string]int{}
}

func (c *Controller) AudioSnapshot() AudioView {
	c.audio.mu.Lock()
	defer c.audio.mu.Unlock()
	return c.audioViewLocked()
}

func (c *Controller) audioViewLocked() AudioView {
	a := &c.audio
	v := AudioView{
		Available:      len(c.deps.Audio.Providers) > 0,
		HistoryEnabled: c.deps.Audio.History != nil, HistoryError: strings.TrimSpace(a.history.err + "\n" + a.history.limit),
		Providers: []AudioProviderOption{},
		Revision:  a.revision,
		Checks:    append([]stt.Check{}, a.checks...),
		Busy:      a.busy,
		Progress:  a.progress,
		Model:     a.model,
		Listening: a.listening,
		Sources:   []AudioSourceView{},
		Segments:  append([]TranscriptSegment{}, a.segments...),
		Error:     a.err,

		SendingUnsent: a.sendingUnsent,
		SendError:     a.sendError,
	}
	for _, s := range a.sources {
		v.Sources = append(v.Sources, s.view)
	}
	for _, p := range c.deps.Audio.Providers {
		d := p.Describe()
		v.Providers = append(v.Providers, AudioProviderOption{ID: d.ID, Label: d.Label, Local: d.Local})
	}
	p := a.provider // the running session's provider
	if p == nil {
		p = c.audioProvider()
	}
	if p != nil {
		v.Provider = p.Describe()
		v.KeyConfigured = a.keys[v.Provider.ID]
	}
	return v
}

// loadAudioKeys records which providers have a stored key; values are never kept here.
func (c *Controller) loadAudioKeys() {
	a := &c.audio
	a.keys = map[string]bool{}
	if c.deps.Audio.Secrets == nil {
		return
	}
	for _, p := range c.deps.Audio.Providers {
		if d := p.Describe(); d.NeedsKey {
			_, ok, _ := c.deps.Audio.Secrets.Get(stt.KeyName(d.ID))
			a.keys[d.ID] = ok
		}
	}
}

func (c *Controller) keyedProvider(id string) (stt.Description, error) {
	for _, p := range c.deps.Audio.Providers {
		if d := p.Describe(); d.ID == id && d.NeedsKey {
			if c.deps.Audio.Secrets == nil {
				return d, errors.New("хранилище ключей недоступно")
			}
			return d, nil
		}
	}
	return stt.Description{}, errors.New("у этого распознавателя нет ключа")
}

// SetSTTKey stores a provider's API key; it does not contact the provider.
func (c *Controller) SetSTTKey(providerID, key string) error {
	d, err := c.keyedProvider(providerID)
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" || len(key) > maxSTTKeyBytes || strings.ContainsAny(key, "\x00\r\n") {
		return fmt.Errorf("ключ %s пустой, длиннее %d байт или содержит переводы строк", d.Label, maxSTTKeyBytes)
	}
	if err := c.deps.Audio.Secrets.Set(stt.KeyName(d.ID), key); err != nil {
		return err
	}
	c.keyChanged(d.ID, true)
	return nil
}

// keyChanged records the key state and re-checks the active provider when nothing else runs.
func (c *Controller) keyChanged(providerID string, stored bool) {
	a := &c.audio
	a.mu.Lock()
	a.keys[providerID] = stored
	c.audioChangedLocked()
	active := c.audioProvider() != nil && c.audioProvider().Describe().ID == providerID && a.busy == "" && a.session == nil
	a.mu.Unlock()
	if active {
		_ = c.RefreshAudio()
	}
}

// DeleteSTTKey forgets a provider's API key.
func (c *Controller) DeleteSTTKey(providerID string) error {
	d, err := c.keyedProvider(providerID)
	if err != nil {
		return err
	}
	if err := c.deps.Audio.Secrets.Delete(stt.KeyName(d.ID)); err != nil {
		return err
	}
	c.keyChanged(d.ID, false)
	return nil
}

// audioProvider is the provider used for the next check, preparation or start: the selected one, or the
// first when the stored choice is empty or no longer exists. Callers hold audio.mu.
func (c *Controller) audioProvider() stt.Provider {
	if len(c.deps.Audio.Providers) == 0 {
		return nil
	}
	if p := c.findProvider(c.audio.providerID); p != nil {
		return p
	}
	return c.deps.Audio.Providers[0]
}

func (c *Controller) findProvider(id string) stt.Provider {
	for _, p := range c.deps.Audio.Providers {
		if p.Describe().ID == id {
			return p
		}
	}
	return nil
}

// SetAudioProvider selects the recognizer; only while nothing is running.
func (c *Controller) SetAudioProvider(id string) error {
	if c.findProvider(id) == nil {
		return errors.New("неизвестный распознаватель")
	}
	a := &c.audio
	a.mu.Lock()
	if a.busy != "" || a.listening || a.model == audioModelReady || a.model == audioModelStarting {
		a.mu.Unlock()
		return errors.New("Сначала остановите распознавание")
	}
	// Busy for the whole switch: no start can pick the old provider while settings are written.
	a.busy = audioBusySwitching
	c.audioChangedLocked()
	a.mu.Unlock()
	c.settingsMu.Lock()
	c.mu.Lock()
	cfg := c.settings
	c.mu.Unlock()
	cfg.Audio.Provider = id
	_, err := c.saveSettingsLocked(cfg)
	c.settingsMu.Unlock()
	a.mu.Lock()
	a.busy = ""
	if err == nil {
		a.providerID, a.checks, a.err, a.model = id, nil, "", audioModelStopped
	}
	c.audioChangedLocked()
	a.mu.Unlock()
	return err
}

// audioChangedLocked bumps the revision and schedules one coalesced solveo:audio event.
func (c *Controller) audioChangedLocked() {
	a := &c.audio
	a.revision++
	if a.changed != nil {
		close(a.changed)
		a.changed = nil
	}
	if a.emitTimer != nil || c.deps.Emit == nil {
		return
	}
	a.emitTimer = time.AfterFunc(audioEmitDelay, func() {
		a.mu.Lock()
		a.emitTimer = nil
		v := c.audioViewLocked()
		a.mu.Unlock()
		c.deps.Emit("solveo:audio", v)
	})
}

func (c *Controller) audioReadyLocked() error {
	a := &c.audio
	if len(c.deps.Audio.Providers) == 0 {
		return errors.New("распознавание речи недоступно")
	}
	switch a.busy {
	case "":
		return nil
	case audioBusyPreparing:
		return errors.New("Подготовка уже идёт")
	case audioBusyChecking:
		return errors.New("Проверка уже идёт")
	case audioBusyStarting:
		return errors.New("Модель уже запускается")
	case audioBusyStopping:
		return errors.New("Модель останавливается")
	default:
		return errors.New("Подождите завершения предыдущей команды")
	}
}

// goAudio runs fn as a controller worker and clears busy afterwards. busy is set by the caller.
func (c *Controller) goAudio(fn func(ctx context.Context)) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		c.audio.mu.Lock()
		c.audio.busy = ""
		c.audioChangedLocked()
		c.audio.mu.Unlock()
		return errors.New("приложение завершается")
	}
	c.workers.Add(1)
	ctx := c.ctx
	c.mu.Unlock()
	go func() {
		defer c.workers.Done()
		fn(ctx)
		c.audio.mu.Lock()
		c.audio.busy, c.audio.progress = "", ""
		c.audioChangedLocked()
		c.audio.mu.Unlock()
	}()
	return nil
}

func uiText(text string) string {
	if r := []rune(text); len(r) > maxUITextRunes {
		return string(r[:maxUITextRunes]) + "…"
	}
	return text
}

func (c *Controller) audioProgress(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	c.appendLog(line + "\n")
	if r := []rune(line); len(r) > maxProgressRunes {
		line = string(r[:maxProgressRunes]) + "…"
	}
	c.audio.mu.Lock()
	c.audio.progress = line
	c.audioChangedLocked()
	c.audio.mu.Unlock()
}

func (c *Controller) refreshAudioChecks(ctx context.Context, provider stt.Provider) {
	checks := provider.Status(ctx)
	view := make([]stt.Check, 0, len(checks))
	for _, ch := range checks {
		ch.Message = uiText(ch.Message)
		view = append(view, ch)
	}
	c.audio.mu.Lock()
	c.audio.checks = view
	c.audioChangedLocked()
	c.audio.mu.Unlock()
}

// beginAudio claims the busy slot and returns the provider the work must use, both under audio.mu.
func (c *Controller) beginAudio(busy string) (stt.Provider, error) {
	a := &c.audio
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := c.audioReadyLocked(); err != nil {
		return nil, err
	}
	a.busy, a.err = busy, ""
	c.audioChangedLocked()
	return c.audioProvider(), nil
}

func (c *Controller) setAudioError(err error) {
	c.appendLog(err.Error() + "\n")
	c.audio.mu.Lock()
	c.audio.err = uiText(err.Error())
	c.audioChangedLocked()
	c.audio.mu.Unlock()
}

// RefreshAudio re-runs the readiness checks in the background.
func (c *Controller) RefreshAudio() error {
	provider, err := c.beginAudio(audioBusyChecking)
	if err != nil {
		return err
	}
	return c.goAudio(func(ctx context.Context) { c.refreshAudioChecks(ctx, provider) })
}

// PrepareAudio builds the image and downloads the model when missing.
func (c *Controller) PrepareAudio() error {
	provider, err := c.beginAudio(audioBusyPreparing)
	if err != nil {
		return err
	}
	return c.goAudio(func(ctx context.Context) {
		if err := provider.Prepare(ctx, c.audioProgress); err != nil {
			c.setAudioError(err)
		}
		c.refreshAudioChecks(ctx, provider)
	})
}

// StartAudioModel starts the container and connects once the model is warmed up.
func (c *Controller) StartAudioModel() error {
	a := &c.audio
	a.mu.Lock()
	if err := c.audioReadyLocked(); err != nil {
		a.mu.Unlock()
		return err
	}
	if a.model == audioModelReady || a.model == audioModelStarting {
		a.mu.Unlock()
		return errors.New("модель уже запущена")
	}
	provider := c.audioProvider()
	// The view describes the provider being started, whatever the selection does meanwhile.
	a.busy, a.model, a.err, a.provider = audioBusyStarting, audioModelStarting, "", provider
	c.audioChangedLocked()
	a.mu.Unlock()
	return c.goAudio(func(ctx context.Context) {
		session, err := provider.Start(ctx, c.audioProgress)
		if err != nil {
			c.appendLog(err.Error() + "\n")
		}
		a.mu.Lock()
		if err != nil {
			a.model, a.err, a.provider = audioModelFailed, uiText(err.Error()), nil
		} else {
			a.model, a.session, a.provider = audioModelReady, session, provider
		}
		c.audioChangedLocked()
		a.mu.Unlock()
		if err == nil {
			c.pumpAudio(session)
		}
	})
}

// pumpAudio applies worker events until the session ends; it runs as a controller worker.
func (c *Controller) pumpAudio(session stt.SessionAPI) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = session.Close()
		return
	}
	c.workers.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.workers.Done()
		var lost error
		for ev := range session.Events() {
			switch ev := ev.(type) {
			case stt.Update:
				c.applyAudioUpdate(ev)
			case stt.StreamError:
				c.audioStreamFailed(ev)
			case error:
				lost = ev
			}
		}
		c.audioSessionEnded(session, lost)
	}()
}

func (c *Controller) audioSessionEnded(session stt.SessionAPI, lost error) {
	a := &c.audio
	a.mu.Lock()
	if a.session != session {
		a.mu.Unlock() // stopped on purpose
		return
	}
	if lost == nil {
		lost = errors.New("распознаватель закрыл соединение")
	}
	provider := a.provider
	c.recordAudioErrorLocked("", lost.Error())
	c.endAudioHistoryLocked()
	a.session, a.provider, a.model, a.err, a.listening = nil, nil, audioModelFailed, uiText(lost.Error()), false
	stops := a.stopSourcesLocked()
	c.audioChangedLocked()
	a.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
	_ = session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), audioStopTimeout)
	defer cancel()
	_ = provider.Stop(ctx) // free the GPU or the connection; a restart is an explicit user action
}

// stopSourcesLocked marks all sources inactive and returns their stop functions.
func (a *audioState) stopSourcesLocked() []func() {
	var stops []func()
	for _, s := range a.sources {
		if s.stop != nil {
			stops = append(stops, s.stop)
			s.stop = nil
		}
		s.view.Active = false
	}
	return stops
}

func (c *Controller) audioStreamFailed(e stt.StreamError) {
	if release := c.pinAudioHistoryStream(e.StreamID); release != nil {
		defer release()
	}
	a := &c.audio
	a.mu.Lock()
	c.recordAudioErrorLocked(e.StreamID, e.Message)
	var stop func()
	active := false
	for _, s := range a.sources {
		if s.stream == e.StreamID {
			stop, s.stop = s.stop, nil
			s.view.Active, s.view.Error = false, uiText(e.Message)
		}
		active = active || s.view.Active
	}
	if !active {
		a.listening = false
		if a.history.streams[e.StreamID] == a.history.current {
			c.endAudioHistoryLocked()
		}
	}
	c.audioChangedLocked()
	a.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// StopAudioModel stops the container; listening must be stopped first.
func (c *Controller) StopAudioModel() error {
	a := &c.audio
	a.mu.Lock()
	if err := c.audioReadyLocked(); err != nil {
		a.mu.Unlock()
		return err
	}
	if a.listening {
		a.mu.Unlock()
		return errors.New("Сначала остановите прослушивание")
	}
	session, provider := a.session, a.provider
	if provider == nil {
		provider = c.audioProvider()
	}
	a.session, a.provider, a.busy, a.err = nil, nil, audioBusyStopping, ""
	c.audioChangedLocked()
	a.mu.Unlock()
	return c.goAudio(func(ctx context.Context) {
		if session != nil {
			_ = session.Close()
		}
		err := provider.Stop(ctx)
		a.mu.Lock()
		a.model = audioModelStopped
		if err != nil {
			a.err = uiText(err.Error())
		}
		c.audioChangedLocked()
		a.mu.Unlock()
	})
}

// StartListening opens one stream per source with the saved dictionary and starts capture.
// A source that cannot start is shown with its error; listening needs at least one source.
func (c *Controller) StartListening() error {
	c.syncAudioHistoryChat()
	a := &c.audio
	a.mu.Lock()
	if len(c.deps.Audio.Providers) > 0 && (a.model != audioModelReady || a.session == nil) {
		a.mu.Unlock()
		return errors.New("Сначала запустите модель")
	}
	if err := c.audioReadyLocked(); err != nil {
		a.mu.Unlock()
		return err
	}
	if a.listening {
		a.mu.Unlock()
		return nil
	}
	session := a.session
	a.busy, a.err, a.gap = audioBusyOpening, "", false
	c.audioChangedLocked()
	a.mu.Unlock()

	c.mu.Lock()
	phrases := slices.Clone(c.settings.Audio.ContextPhrases)
	ctx := c.ctx
	c.mu.Unlock()
	var sources []*audioSource
	var firstErr error
	a.mu.Lock()
	historyProvider := a.provider
	if historyProvider == nil {
		historyProvider = c.audioProvider()
	}
	description := historyProvider.Describe()
	a.history.runID = chat.ID()
	historyID := c.beginAudioHistoryLocked(description.ID, description.Label, phrases)
	a.mu.Unlock()
	for _, src := range []audio.Source{audio.System, audio.Mic} {
		s := &audioSource{view: AudioSourceView{Track: string(src)}, stream: chat.ID()}
		sources = append(sources, s)
		a.mu.Lock()
		a.tracks[s.stream] = string(src)
		if historyID != "" {
			a.history.streams[s.stream] = a.history.current
			a.history.streamRuns[s.stream] = a.history.runID
			a.history.streamChats[s.stream] = a.history.chatID
		}
		a.mu.Unlock()
		err := session.Open(ctx, s.stream, string(src), phrases)
		if err == nil {
			var dev audio.Device
			dev, s.stop, err = c.deps.Audio.Capturer.Start(ctx, src, c.audioSink(session, s.stream), c.audioCaptureFailed(s.stream))
			if err != nil {
				_ = session.CloseStream(s.stream)
			}
			s.view.Device = dev.Name
		}
		if err != nil {
			s.view.Error = uiText(err.Error())
			firstErr = cmpErr(firstErr, err)
			continue
		}
		s.view.Active = true
	}
	a.mu.Lock()
	a.busy, a.sources = "", sources
	for _, s := range sources {
		a.listening = a.listening || s.view.Active
	}
	listening := a.listening
	for _, doc := range a.history.docs {
		if doc.runID == a.history.runID {
			doc.session.Sources = nil
			for _, source := range sources {
				doc.session.Sources = append(doc.session.Sources, audiohistory.Source{StreamID: source.stream, Track: source.view.Track, Device: source.view.Device})
			}
			doc.revision++
			a.history.persist.mark()
		}
	}
	for _, source := range sources {
		if source.view.Error != "" {
			c.recordAudioErrorLocked(source.stream, source.view.Error)
		}
	}
	if !listening {
		c.endAudioHistoryLocked()
	}
	c.audioChangedLocked()
	a.mu.Unlock()
	if !listening {
		return firstErr
	}
	return nil
}

func cmpErr(first, next error) error {
	if first != nil {
		return first
	}
	return next
}

func (c *Controller) audioSink(session stt.SessionAPI, stream string) func(audio.Frame) {
	return func(f audio.Frame) {
		if err := session.Push(stream, f.Rate, f.Channels, f.Samples); errors.Is(err, stt.ErrBackpressure) {
			c.audio.mu.Lock()
			if !c.audio.gap {
				c.audio.gap, c.audio.err = true, gapMessage
				c.audioChangedLocked()
			}
			c.audio.mu.Unlock()
		}
	}
}

func (c *Controller) audioCaptureFailed(stream string) func(error) {
	return func(err error) {
		a := &c.audio
		a.mu.Lock()
		defer a.mu.Unlock()
		c.recordAudioErrorLocked(stream, err.Error())
		active := false
		for _, s := range a.sources {
			if s.stream == stream {
				s.view.Active, s.view.Error = false, uiText(err.Error())
			}
			active = active || s.view.Active
		}
		if !active {
			a.listening = false
			if a.history.streams[stream] == a.history.current {
				c.endAudioHistoryLocked()
			}
		}
		c.audioChangedLocked()
	}
}

// StopListening stops capture at once; the worker finishes the last phrase of each stream in the background.
func (c *Controller) StopListening() error {
	a := &c.audio
	a.mu.Lock()
	session := a.session
	streams := make([]string, 0, len(a.sources))
	for _, s := range a.sources {
		streams = append(streams, s.stream)
	}
	stops := a.stopSourcesLocked()
	wasListening := a.listening
	a.listening = false
	c.endAudioHistoryLocked()
	c.audioChangedLocked()
	a.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
	if !wasListening || session == nil {
		return nil
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.workers.Add(1)
	ctx := c.ctx
	c.mu.Unlock()
	go func() {
		defer c.workers.Done()
		for _, stream := range streams {
			flushCtx, cancel := context.WithTimeout(ctx, audioFlushTimeout)
			err := session.Flush(flushCtx, stream)
			cancel()
			if err != nil && ctx.Err() == nil {
				c.audioStreamFailed(stt.StreamError{StreamID: stream, Message: "Не удалось завершить распознавание: " + err.Error()})
			}
			_ = session.CloseStream(stream)
		}
	}()
	return nil
}

func (c *Controller) applyAudioUpdate(u stt.Update) {
	var observed *questionbuilder.Segment
	var ownerChat string
	defer func() {
		if observed != nil {
			c.observeQuestion(ownerChat, *observed)
		}
	}()
	c.syncAudioHistoryChat()
	c.bindAudioHistoryUtterance(u.StreamID, u.UtteranceID)
	if u.Kind == stt.KindFinal {
		if release := c.pinAudioHistoryUtterance(u.StreamID, u.UtteranceID); release != nil {
			defer release()
		}
	}
	a := &c.audio
	a.mu.Lock()
	defer a.mu.Unlock()
	track, ok := a.tracks[u.StreamID]
	if !ok {
		return
	}
	if owner := a.history.utteranceChats[u.UtteranceID]; owner != "" && owner != a.history.chatID {
		ownerChat = owner
		at := a.history.started[u.UtteranceID]
		if at == "" {
			at = c.deps.Now().UTC().Format(time.RFC3339Nano)
		}
		observed = &questionbuilder.Segment{ID: u.UtteranceID, Track: track, Text: strings.TrimSpace(u.Text), At: at, Final: u.Kind == stt.KindFinal}
		if u.Kind == stt.KindFinal {
			c.recordAudioFinalLocked(u.StreamID, TranscriptSegment{ID: u.UtteranceID, Track: track, StartedAt: a.history.started[u.UtteranceID], Text: strings.TrimSpace(u.Text), Final: true})
			delete(a.history.started, u.UtteranceID)
		}
		return
	}

	i := slices.IndexFunc(a.segments, func(s TranscriptSegment) bool { return s.ID == u.UtteranceID })
	if i >= 0 && (a.segments[i].Final || u.Revision <= a.revisions[u.UtteranceID]) && u.Kind != stt.KindStarted {
		return
	}
	ownerChat = a.history.chatID
	if ownerChat == "" {
		ownerChat = a.history.utteranceChats[u.UtteranceID]
	}
	at, _ := time.Parse(time.RFC3339, a.history.started[u.UtteranceID])
	if at.IsZero() {
		at = c.deps.Now().UTC()
	}
	observed = &questionbuilder.Segment{ID: u.UtteranceID, Track: track, Text: strings.TrimSpace(u.Text), At: at.UTC().Format(time.RFC3339Nano), Final: u.Kind == stt.KindFinal}
	if u.Kind == stt.KindFinal && strings.TrimSpace(u.Text) == "" {
		if i >= 0 {
			a.segments = slices.Delete(a.segments, i, i+1)
			delete(a.revisions, u.UtteranceID)
			c.audioChangedLocked()
		}
		return
	}
	if i < 0 {
		started := a.history.started[u.UtteranceID]
		if started == "" {
			started = c.deps.Now().UTC().Format(time.RFC3339)
		}
		a.segments = append(a.segments, TranscriptSegment{ID: u.UtteranceID, Track: track, StartedAt: started})
		i = len(a.segments) - 1
	}
	if u.Kind != stt.KindStarted {
		a.segments[i].Text = strings.TrimSpace(u.Text)
		a.segments[i].Final = u.Kind == stt.KindFinal
		a.revisions[u.UtteranceID] = u.Revision
	}
	if u.Kind == stt.KindFinal {
		c.recordAudioFinalLocked(u.StreamID, a.segments[i])
		delete(a.history.started, u.UtteranceID)
	}
	if u.Kind == stt.KindFinal && a.gap {
		a.gap = false
		if a.err == gapMessage {
			a.err = ""
		}
	}
	for len(a.segments) > maxTranscriptSegments {
		delete(a.revisions, a.segments[0].ID)
		delete(a.history.segments, a.segments[0].ID)
		a.segments = slices.Delete(a.segments, 0, 1)
	}
	c.audioChangedLocked()
}

// ClearTranscript drops every phrase; recognition continues.
func (c *Controller) ClearTranscript() error {
	c.audio.mu.Lock()
	defer c.audio.mu.Unlock()
	c.audio.segments, c.audio.revisions = nil, map[string]int{}
	c.audio.history.segments = map[string]string{}
	c.audioChangedLocked()
	return nil
}

// SubmitTranscript sends the selected final phrases, in transcript order, as an ordinary question of the
// active chat. The composer draft and queued screenshots are never read or changed.
func (c *Controller) SubmitTranscript(in SubmitTranscriptInput) (string, error) {
	return c.submitSegments(in, false)
}

// submitSegments sends final phrases; consume also removes them from the question builder's input.
func (c *Controller) submitSegments(in SubmitTranscriptInput, consume bool) (string, error) {
	if len(in.SegmentIDs) == 0 {
		return "", ErrEmpty
	}
	wanted := map[string]bool{}
	for _, id := range in.SegmentIDs {
		wanted[id] = true
	}
	c.audio.mu.Lock()
	var texts, ids []string
	groups := map[string][]string{}
	for _, s := range c.audio.segments {
		if !wanted[s.ID] {
			continue
		}
		delete(wanted, s.ID)
		if !s.Final {
			c.audio.mu.Unlock()
			return "", errors.New("Фраза ещё распознаётся")
		}
		texts = append(texts, s.Text)
		ids = append(ids, s.ID)
		if id := c.audio.history.segments[s.ID]; id != "" {
			groups[id] = append(groups[id], s.ID)
		}
	}
	c.audio.mu.Unlock()
	if len(wanted) != 0 {
		return "", errors.New("Фраза больше недоступна")
	}
	operationID, err := c.submit(SubmitInput{ChatID: in.ChatID, RequestID: in.RequestID, Text: strings.Join(texts, "\n")}, submitOrigin{kind: originTranscript, segments: ids, consume: consume})
	if err == nil {
		c.recordAudioSubmission(groups, in.ChatID, operationID)
	}
	return operationID, err
}

// markTranscriptSent flags live phrases included in an accepted submission. Called without c.mu.
func (c *Controller) markTranscriptSent(ids []string) {
	if len(ids) == 0 {
		return
	}
	a := &c.audio
	a.mu.Lock()
	defer a.mu.Unlock()
	changed := false
	for i := range a.segments {
		if !a.segments[i].Sent && slices.Contains(ids, a.segments[i].ID) {
			a.segments[i].Sent = true
			changed = true
		}
	}
	if changed {
		c.audioChangedLocked()
	}
}

// shutdownAudio stops capture, the session and Solveo's container; called by Close.
func (c *Controller) shutdownAudio(ctx context.Context) {
	a := &c.audio
	a.mu.Lock()
	session, provider := a.session, a.provider
	streams := make([]string, 0, len(a.sources))
	for _, source := range a.sources {
		if source.stop != nil || source.view.Active {
			streams = append(streams, source.stream)
		}
	}
	stops := a.stopSourcesLocked()
	c.endAudioHistoryLocked()
	a.session, a.provider, a.listening, a.model = nil, nil, false, audioModelStopped
	if a.emitTimer != nil {
		a.emitTimer.Stop()
		a.emitTimer = nil
	}
	a.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
	if session == nil {
		return
	}
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), audioStopTimeout)
	defer cancel()
	for _, stream := range streams {
		if err := session.Flush(stopCtx, stream); err != nil {
			c.audio.mu.Lock()
			c.recordAudioErrorLocked(stream, err.Error())
			c.audio.mu.Unlock()
		}
	}
	_ = session.Close()
	if err := provider.Stop(stopCtx); err != nil {
		c.appendLog("solveo-stt: " + err.Error() + "\n")
	}
}
