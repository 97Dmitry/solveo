package grok

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/97Dmitry/solveo/internal/stt"
)

const (
	maxQueued   = 50 // ≈ 5 s of 100 ms frames
	eventBuffer = 256
)

var allowedRates = map[int]bool{8000: true, 16000: true, 22050: true, 24000: true, 44100: true, 48000: true}

type outbound struct {
	typ  websocket.MessageType
	data []byte
}

type stream struct {
	id, track string
	phrases   []string
	out       chan outbound
	finals    chan struct{} // one signal per utterance final, for Flush
	rate      int
	running   bool
	failed    bool
}

// Session holds one xAI WebSocket per stream, dialled on the first frame when the device rate is known.
type Session struct {
	provider  *Provider
	key       string
	flushWait time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	events chan any
	once   sync.Once

	mu      sync.Mutex
	streams map[string]*stream
}

func newSession(p *Provider, key string) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	return &Session{provider: p, key: key, flushWait: defaultFlushWait, ctx: ctx, cancel: cancel,
		events: make(chan any, eventBuffer), streams: map[string]*stream{}}
}

func (s *Session) Events() <-chan any { return s.events }

func (s *Session) emit(ev any) {
	select {
	case s.events <- ev:
	case <-s.ctx.Done():
	}
}

func (s *Session) fail(st *stream, message string) {
	s.mu.Lock()
	already := st.failed
	st.failed = true
	s.mu.Unlock()
	if !already {
		s.emit(stt.StreamError{StreamID: st.id, Message: message})
	}
}

func (s *Session) Open(_ context.Context, streamID, track string, phrases []string) error {
	if len(phrases) > maxKeyTerms {
		return fmt.Errorf("словарь для Grok больше %d терминов", maxKeyTerms)
	}
	for _, p := range phrases {
		if utf8.RuneCountInString(p) > maxTermRunes {
			return fmt.Errorf("термин «%s» длиннее %d символов, Grok его не примет", p, maxTermRunes)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[streamID]; ok {
		return errors.New("поток уже открыт")
	}
	s.streams[streamID] = &stream{id: streamID, track: track, phrases: append([]string(nil), phrases...),
		out: make(chan outbound, maxQueued+16), finals: make(chan struct{}, 1)}
	return nil
}

// pcm16 downmixes interleaved float32 to mono signed 16-bit little endian.
func pcm16(samples []float32, channels int) []byte {
	frames := len(samples) / channels
	out := make([]byte, 2*frames)
	for i := range frames {
		var sum float32
		for c := range channels {
			sum += samples[i*channels+c]
		}
		v := math.Max(-1, math.Min(1, float64(sum/float32(channels))))
		binary.LittleEndian.PutUint16(out[2*i:], uint16(int16(math.Round(v*32767))))
	}
	return out
}

func (s *Session) Push(streamID string, rate, channels int, samples []float32) error {
	s.mu.Lock()
	st, ok := s.streams[streamID]
	if !ok || st.failed {
		s.mu.Unlock()
		return nil
	}
	if !st.running {
		if !allowedRates[rate] {
			s.mu.Unlock()
			s.fail(st, fmt.Sprintf("частота %d Гц не поддерживается Grok; выберите 44100 или 48000 Гц в настройках звука Windows", rate))
			return nil
		}
		st.rate, st.running = rate, true
		s.wg.Add(1)
		go s.run(st)
	}
	s.mu.Unlock()
	if len(st.out) >= maxQueued {
		return stt.ErrBackpressure
	}
	select {
	case st.out <- outbound{websocket.MessageBinary, pcm16(samples, channels)}:
		return nil
	default:
		return stt.ErrBackpressure
	}
}

func (s *Session) control(ctx context.Context, st *stream, msg map[string]any) error {
	data, _ := json.Marshal(msg)
	select {
	case st.out <- outbound{websocket.MessageText, data}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ctx.Done():
		return errors.New("сессия распознавания закрыта")
	}
}

// Flush asks xAI to finish the current utterance and waits briefly for it; silence means nothing was pending.
func (s *Session) Flush(ctx context.Context, streamID string) error {
	s.mu.Lock()
	st, ok := s.streams[streamID]
	running := ok && st.running && !st.failed
	s.mu.Unlock()
	if !running {
		return nil
	}
	select {
	case <-st.finals:
	default:
	}
	if err := s.control(ctx, st, map[string]any{"type": "Finalize"}); err != nil {
		return err
	}
	select {
	case <-st.finals:
	case <-time.After(s.flushWait):
	case <-ctx.Done():
	}
	return nil
}

func (s *Session) CloseStream(streamID string) error {
	s.mu.Lock()
	st, ok := s.streams[streamID]
	delete(s.streams, streamID)
	s.mu.Unlock()
	if !ok || !st.running {
		return nil
	}
	ctx, cancel := context.WithTimeout(s.ctx, dialTimeout)
	defer cancel()
	return s.control(ctx, st, map[string]any{"type": "audio.done"})
}

func (s *Session) Close() error {
	s.once.Do(func() {
		s.cancel()
		s.wg.Wait()
		close(s.events)
	})
	return nil
}

func (s *Session) run(st *stream) {
	defer s.wg.Done()
	query := baseQuery(st.rate)
	query.Set("interim_results", "true")
	query.Set("endpointing", endpointingMs)
	for _, p := range st.phrases {
		query.Add("keyterm", p)
	}
	conn, err := s.provider.handshake(s.ctx, s.key, query)
	if err != nil {
		s.fail(st, err.Error())
		return
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = conn.CloseNow()
	}()
	readCtx, readCancel := context.WithTimeout(ctx, dialTimeout)
	_, first, err := conn.Read(readCtx)
	readCancel()
	if err != nil || !strings.Contains(string(first), "transcript.created") {
		s.fail(st, "xAI не начал сессию распознавания")
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.read(ctx, conn, st)
		cancel()
	}()
	for {
		select {
		case <-ctx.Done():
			<-done
			return
		case m := <-st.out:
			if err := conn.Write(ctx, m.typ, m.data); err != nil {
				cancel()
			}
		}
	}
}

type xaiEvent struct {
	Type        string  `json:"type"`
	Text        string  `json:"text"`
	IsFinal     bool    `json:"is_final"`
	SpeechFinal bool    `json:"speech_final"`
	Start       float64 `json:"start"`
	Duration    float64 `json:"duration"`
	Message     string  `json:"message"`
}

func (s *Session) read(ctx context.Context, conn *websocket.Conn, st *stream) {
	a := &assembler{stream: st.id}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if s.ctx.Err() == nil && !s.closing(st) {
				s.fail(st, "соединение с xAI прервано")
			}
			return
		}
		var ev xaiEvent
		if json.Unmarshal(data, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "transcript.partial":
			for _, u := range a.apply(ev) {
				s.emit(u)
			}
			if ev.SpeechFinal {
				select {
				case st.finals <- struct{}{}:
				default:
				}
			}
		case "error":
			s.fail(st, "xAI: "+ev.Message)
			return
		}
	}
}

// closing reports whether the stream was closed on purpose (CloseStream), so its socket end is expected.
func (s *Session) closing(st *stream) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, open := s.streams[st.id]
	return !open
}

// assembler turns xAI's interim / chunk-final / utterance-final results into stt updates of one stream.
// xAI does not document whether a final repeats the locked chunks, so both forms are accepted.
type assembler struct {
	stream string
	n, rev int
	active bool
	locked string
}

func join(a, b string) string {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + " " + b
}

func (a *assembler) with(text string) string {
	text = strings.TrimSpace(text)
	if a.locked != "" && strings.HasPrefix(text, a.locked) {
		return text
	}
	return join(a.locked, text)
}

func (a *assembler) update(kind, text string, ev xaiEvent) stt.Update {
	a.rev++
	if kind == stt.KindStarted {
		a.rev = 0
	}
	start := int64(ev.Start * 16000)
	return stt.Update{StreamID: a.stream, UtteranceID: fmt.Sprintf("%s:%d", a.stream, a.n), Revision: a.rev, Kind: kind,
		StartSample: start, EndSample: start + int64(ev.Duration*16000), Text: text}
}

func (a *assembler) apply(ev xaiEvent) []stt.Update {
	if strings.TrimSpace(ev.Text) == "" && !ev.SpeechFinal {
		return nil
	}
	var out []stt.Update
	if !a.active {
		if strings.TrimSpace(ev.Text) == "" {
			return nil // a final with nothing pending
		}
		a.n, a.active, a.locked = a.n+1, true, ""
		out = append(out, a.update(stt.KindStarted, "", ev))
	}
	text := a.with(ev.Text)
	switch {
	case ev.SpeechFinal:
		out = append(out, a.update(stt.KindFinal, text, ev))
		a.active, a.locked = false, ""
	case ev.IsFinal:
		a.locked = text
		out = append(out, a.update(stt.KindPartial, text, ev))
	default:
		out = append(out, a.update(stt.KindPartial, text, ev))
	}
	return out
}

var _ stt.SessionAPI = (*Session)(nil)
