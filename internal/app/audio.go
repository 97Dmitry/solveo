package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/97Dmitry/solveo/internal/audio"
	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/stt"
	sttdocker "github.com/97Dmitry/solveo/internal/stt/docker"
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

type AudioRuntime interface {
	Status(ctx context.Context) []sttdocker.Check
	Prepare(ctx context.Context, progress func(string)) error
	Start(ctx context.Context, progress func(string)) (sttdocker.Endpoint, error)
	Stop(ctx context.Context) error
}

// STTSession is the worker connection (stt.Session in production).
type STTSession interface {
	Open(ctx context.Context, streamID, track string, phrases []string) error
	Push(streamID string, rate, channels int, samples []float32) error
	Flush(ctx context.Context, streamID string) error
	CloseStream(streamID string) error
	Events() <-chan any
	Close() error
}

// AudioDeps is optional: a nil Runtime leaves speech recognition unavailable.
type AudioDeps struct {
	Runtime  AudioRuntime
	Dial     func(ctx context.Context, addr, token string) (STTSession, error)
	Capturer audio.Capturer
}

type AudioCheck struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message"`
}

type AudioSourceView struct {
	Track  string `json:"track"`
	Device string `json:"device"`
	Active bool   `json:"active"`
	Error  string `json:"error"`
}

// TranscriptSegment is one utterance; its ID is the worker's utterance ID.
type TranscriptSegment struct {
	ID        string `json:"id"`
	Track     string `json:"track"`
	StartedAt string `json:"started_at"`
	Text      string `json:"text"`
	Final     bool   `json:"final"`
}

type AudioView struct {
	Available bool                `json:"available"`
	Revision  uint64              `json:"revision"`
	Checks    []AudioCheck        `json:"checks"`
	Busy      string              `json:"busy"`
	Progress  string              `json:"progress"`
	Model     string              `json:"model"`
	Listening bool                `json:"listening"`
	Sources   []AudioSourceView   `json:"sources"`
	Segments  []TranscriptSegment `json:"segments"`
	Error     string              `json:"error"`
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
	mu        sync.Mutex
	revision  uint64
	checks    []AudioCheck
	busy      string
	progress  string
	model     string
	err       string
	gap       bool
	session   STTSession
	listening bool
	sources   []*audioSource
	tracks    map[string]string // stream ID → track, kept after stop for late finals
	segments  []TranscriptSegment
	revisions map[string]int
	emitTimer *time.Timer
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
		Available: c.deps.Audio.Runtime != nil,
		Revision:  a.revision,
		Checks:    append([]AudioCheck{}, a.checks...),
		Busy:      a.busy,
		Progress:  a.progress,
		Model:     a.model,
		Listening: a.listening,
		Sources:   []AudioSourceView{},
		Segments:  append([]TranscriptSegment{}, a.segments...),
		Error:     a.err,
	}
	for _, s := range a.sources {
		v.Sources = append(v.Sources, s.view)
	}
	return v
}

// audioChangedLocked bumps the revision and schedules one coalesced solveo:audio event.
func (c *Controller) audioChangedLocked() {
	a := &c.audio
	a.revision++
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
	if c.deps.Audio.Runtime == nil {
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

func (c *Controller) refreshAudioChecks(ctx context.Context) {
	checks := c.deps.Audio.Runtime.Status(ctx)
	view := make([]AudioCheck, 0, len(checks))
	for _, ch := range checks {
		ch.Message = uiText(ch.Message)
		view = append(view, AudioCheck(ch))
	}
	c.audio.mu.Lock()
	c.audio.checks = view
	c.audioChangedLocked()
	c.audio.mu.Unlock()
}

func (c *Controller) beginAudio(busy string) error {
	a := &c.audio
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := c.audioReadyLocked(); err != nil {
		return err
	}
	a.busy, a.err = busy, ""
	c.audioChangedLocked()
	return nil
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
	if err := c.beginAudio(audioBusyChecking); err != nil {
		return err
	}
	return c.goAudio(c.refreshAudioChecks)
}

// PrepareAudio builds the image and downloads the model when missing.
func (c *Controller) PrepareAudio() error {
	if err := c.beginAudio(audioBusyPreparing); err != nil {
		return err
	}
	return c.goAudio(func(ctx context.Context) {
		if err := c.deps.Audio.Runtime.Prepare(ctx, c.audioProgress); err != nil {
			c.setAudioError(err)
		}
		c.refreshAudioChecks(ctx)
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
	a.busy, a.model, a.err = audioBusyStarting, audioModelStarting, ""
	c.audioChangedLocked()
	a.mu.Unlock()
	return c.goAudio(func(ctx context.Context) {
		runtime := c.deps.Audio.Runtime
		ep, err := runtime.Start(ctx, c.audioProgress)
		var session STTSession
		if err == nil {
			session, err = c.deps.Audio.Dial(ctx, ep.Addr, ep.Token)
			if err != nil {
				stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), audioStopTimeout)
				_ = runtime.Stop(stopCtx)
				cancel()
			}
		}
		if err != nil {
			c.appendLog(err.Error() + "\n")
		}
		a.mu.Lock()
		if err != nil {
			a.model, a.err = audioModelFailed, uiText(err.Error())
		} else {
			a.model, a.session = audioModelReady, session
		}
		c.audioChangedLocked()
		a.mu.Unlock()
		if err == nil {
			c.pumpAudio(session)
		}
	})
}

// pumpAudio applies worker events until the session ends; it runs as a controller worker.
func (c *Controller) pumpAudio(session STTSession) {
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

func (c *Controller) audioSessionEnded(session STTSession, lost error) {
	a := &c.audio
	a.mu.Lock()
	if a.session != session {
		a.mu.Unlock() // stopped on purpose
		return
	}
	if lost == nil {
		lost = errors.New("распознаватель закрыл соединение")
	}
	a.session, a.model, a.err, a.listening = nil, audioModelFailed, uiText(lost.Error()), false
	stops := a.stopSourcesLocked()
	c.audioChangedLocked()
	a.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
	_ = session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), audioStopTimeout)
	defer cancel()
	_ = c.deps.Audio.Runtime.Stop(ctx) // free the GPU; a restart is an explicit user action
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
	a := &c.audio
	a.mu.Lock()
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
	session := a.session
	a.session, a.busy, a.err = nil, audioBusyStopping, ""
	c.audioChangedLocked()
	a.mu.Unlock()
	return c.goAudio(func(ctx context.Context) {
		if session != nil {
			_ = session.Close()
		}
		err := c.deps.Audio.Runtime.Stop(ctx)
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
	a := &c.audio
	a.mu.Lock()
	if c.deps.Audio.Runtime != nil && (a.model != audioModelReady || a.session == nil) {
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
	for _, src := range []audio.Source{audio.System, audio.Mic} {
		s := &audioSource{view: AudioSourceView{Track: string(src)}, stream: chat.ID()}
		sources = append(sources, s)
		a.mu.Lock()
		a.tracks[s.stream] = string(src)
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

func (c *Controller) audioSink(session STTSession, stream string) func(audio.Frame) {
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
		active := false
		for _, s := range a.sources {
			if s.stream == stream {
				s.view.Active, s.view.Error = false, uiText(err.Error())
			}
			active = active || s.view.Active
		}
		if !active {
			a.listening = false
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
			_ = session.Flush(flushCtx, stream)
			cancel()
			_ = session.CloseStream(stream)
		}
	}()
	return nil
}

func (c *Controller) applyAudioUpdate(u stt.Update) {
	a := &c.audio
	a.mu.Lock()
	defer a.mu.Unlock()
	track, ok := a.tracks[u.StreamID]
	if !ok {
		return
	}
	i := slices.IndexFunc(a.segments, func(s TranscriptSegment) bool { return s.ID == u.UtteranceID })
	if i >= 0 && (a.segments[i].Final || u.Revision <= a.revisions[u.UtteranceID]) && u.Kind != stt.KindStarted {
		return
	}
	if u.Kind == stt.KindFinal && strings.TrimSpace(u.Text) == "" {
		if i >= 0 {
			a.segments = slices.Delete(a.segments, i, i+1)
			delete(a.revisions, u.UtteranceID)
			c.audioChangedLocked()
		}
		return
	}
	if i < 0 {
		a.segments = append(a.segments, TranscriptSegment{ID: u.UtteranceID, Track: track, StartedAt: c.deps.Now().UTC().Format(time.RFC3339)})
		i = len(a.segments) - 1
	}
	if u.Kind != stt.KindStarted {
		a.segments[i].Text = strings.TrimSpace(u.Text)
		a.segments[i].Final = u.Kind == stt.KindFinal
		a.revisions[u.UtteranceID] = u.Revision
	}
	if u.Kind == stt.KindFinal && a.gap {
		a.gap = false
		if a.err == gapMessage {
			a.err = ""
		}
	}
	for len(a.segments) > maxTranscriptSegments {
		delete(a.revisions, a.segments[0].ID)
		a.segments = slices.Delete(a.segments, 0, 1)
	}
	c.audioChangedLocked()
}

// ClearTranscript drops every phrase; recognition continues.
func (c *Controller) ClearTranscript() error {
	c.audio.mu.Lock()
	defer c.audio.mu.Unlock()
	c.audio.segments, c.audio.revisions = nil, map[string]int{}
	c.audioChangedLocked()
	return nil
}

// SubmitTranscript sends the selected final phrases, in transcript order, as an ordinary question of the
// active chat. The composer draft and queued screenshots are never read or changed.
func (c *Controller) SubmitTranscript(in SubmitTranscriptInput) (string, error) {
	if len(in.SegmentIDs) == 0 {
		return "", ErrEmpty
	}
	wanted := map[string]bool{}
	for _, id := range in.SegmentIDs {
		wanted[id] = true
	}
	c.audio.mu.Lock()
	var texts []string
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
	}
	c.audio.mu.Unlock()
	if len(wanted) != 0 {
		return "", errors.New("Фраза больше недоступна")
	}
	return c.submit(SubmitInput{ChatID: in.ChatID, RequestID: in.RequestID, Text: strings.Join(texts, "\n")}, submitOrigin{kind: originTranscript})
}

// shutdownAudio stops capture, the session and Solveo's container; called by Close.
func (c *Controller) shutdownAudio(ctx context.Context) {
	a := &c.audio
	a.mu.Lock()
	session := a.session
	stops := a.stopSourcesLocked()
	a.session, a.listening, a.model = nil, false, audioModelStopped
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
	_ = session.Close()
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), audioStopTimeout)
	defer cancel()
	if err := c.deps.Audio.Runtime.Stop(stopCtx); err != nil {
		c.appendLog("solveo-stt: " + err.Error() + "\n")
	}
}
