package gemini

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

const maxQueued = 50

type outbound struct {
	data    []byte
	written chan error
}
type stream struct {
	id                    string
	conn                  *websocket.Conn
	ctx                   context.Context
	cancel                context.CancelFunc
	out                   chan outbound
	finals                chan struct{}
	ready, failed, closed bool // protected by Session.mu
	hasPartial            bool // protected by Session.mu; unresolved interim text
}

type Session struct {
	provider *Provider
	key      string
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	streams  map[string]*stream
	closed   bool
	wg       sync.WaitGroup
	events   chan any
	once     sync.Once
}

func newSession(p *Provider, key string) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	return &Session{provider: p, key: key, ctx: ctx, cancel: cancel, streams: map[string]*stream{}, events: make(chan any, 256)}
}
func (s *Session) Events() <-chan any { return s.events }
func (s *Session) emit(v any) {
	select {
	case s.events <- v:
	case <-s.ctx.Done():
	}
}
func (s *Session) fail(st *stream, message string) {
	s.mu.Lock()
	report := !st.failed && !st.closed && !s.closed
	st.failed = true
	s.mu.Unlock()
	st.cancel()
	if report {
		s.emit(stt.StreamError{StreamID: st.id, Message: message})
	}
}
func (s *Session) Open(ctx context.Context, id, _ string, phrases []string) error {
	if len(phrases) > maxPhrases {
		return fmt.Errorf("Словарь Gemini превышает %d терминов", maxPhrases)
	}
	for _, p := range phrases {
		if !utf8.ValidString(p) || strings.ContainsRune(p, 0) || strings.TrimSpace(p) == "" || utf8.RuneCountInString(p) > maxPhraseRunes {
			return errors.New("Некорректный термин в словаре Gemini")
		}
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("Сессия распознавания закрыта")
	}
	if _, ok := s.streams[id]; ok {
		s.mu.Unlock()
		return errors.New("Поток уже открыт")
	}
	streamCtx, cancel := context.WithCancel(s.ctx)
	st := &stream{id: id, ctx: streamCtx, cancel: cancel, out: make(chan outbound, maxQueued), finals: make(chan struct{}, 1)}
	s.streams[id] = st
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	stop := context.AfterFunc(ctx, cancel)
	conn, err := s.provider.connect(streamCtx, s.key, append([]string(nil), phrases...))
	stop()
	if err != nil {
		s.mu.Lock()
		if s.streams[id] == st {
			delete(s.streams, id)
		}
		s.mu.Unlock()
		cancel()
		return err
	}
	s.mu.Lock()
	if s.closed || st.closed || streamCtx.Err() != nil {
		s.mu.Unlock()
		_ = conn.CloseNow()
		cancel()
		return errors.New("Открытие потока Gemini отменено")
	}
	st.conn, st.ready = conn, true
	s.wg.Add(2)
	s.mu.Unlock()
	go s.write(st)
	go s.read(st)
	return nil
}

// pcm16 downmixes the device's interleaved float samples. Google resamples the declared rate.
func pcm16(samples []float32, channels int) []byte {
	b := make([]byte, len(samples)/channels*2)
	for i := range len(b) / 2 {
		sum := float64(0)
		for c := range channels {
			v := float64(samples[i*channels+c])
			if !math.IsNaN(v) {
				sum += v
			}
		}
		v := math.Max(-1, math.Min(1, sum/float64(channels)))
		binary.LittleEndian.PutUint16(b[2*i:], uint16(int16(math.Round(v*32767))))
	}
	return b
}
func (s *Session) Push(id string, rate, channels int, samples []float32) error {
	if rate < 8000 || rate > 192000 || channels < 1 || channels > 32 || len(samples)%channels != 0 {
		return errors.New("Некорректный формат звука Gemini")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.streams[id]
	if s.closed || st == nil || !st.ready || st.failed || st.closed {
		return nil
	}
	// []byte marshals to base64; MIME always matches the device rate.
	b, _ := json.Marshal(map[string]any{"realtimeInput": map[string]any{"audio": map[string]any{"data": pcm16(samples, channels), "mimeType": fmt.Sprintf("audio/pcm;rate=%d", rate)}}})
	select {
	case st.out <- outbound{data: b}:
		return nil
	default:
		return stt.ErrBackpressure
	}
}
func (s *Session) Flush(ctx context.Context, id string) error {
	s.mu.Lock()
	st := s.streams[id]
	running := st != nil && st.ready && !st.failed && !st.closed && !s.closed
	s.mu.Unlock()
	if !running {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	select {
	case <-st.finals:
	default:
	}
	msg := outbound{data: []byte(`{"realtimeInput":{"audioStreamEnd":true}}`), written: make(chan error, 1)}
	select {
	case st.out <- msg:
	case <-ctx.Done():
		return ctx.Err()
	case <-st.ctx.Done():
		return errors.New("Поток Gemini закрыт")
	}
	select {
	case err := <-msg.written:
		if err != nil {
			return errors.New("Не удалось завершить аудиопоток Gemini")
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-st.ctx.Done():
		return errors.New("Поток Gemini закрыт")
	}
	for {
		select {
		case <-st.finals:
			s.mu.Lock()
			pending := st.hasPartial
			s.mu.Unlock()
			if !pending {
				return nil
			}
			// turnComplete alone does not confirm an outstanding transcription.
		case <-st.ctx.Done():
			return errors.New("Поток Gemini закрыт")
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				s.mu.Lock()
				pending := st.hasPartial
				s.mu.Unlock()
				if pending {
					return errors.New("Gemini не подтвердил последнюю фразу: время ожидания истекло; промежуточный текст не отправлен")
				}
				return nil
			}
			return ctx.Err()
		}
	}
}
func (s *Session) CloseStream(id string) error {
	s.mu.Lock()
	st := s.streams[id]
	delete(s.streams, id)
	if st != nil {
		st.closed = true
	}
	s.mu.Unlock()
	if st != nil {
		st.cancel()
	}
	return nil
}
func (s *Session) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.cancel()
		s.mu.Unlock()
		s.wg.Wait()
		close(s.events)
	})
	return nil
}
func (s *Session) write(st *stream) {
	defer s.wg.Done()
	defer func() { _ = st.conn.CloseNow() }()
	for {
		select {
		case <-st.ctx.Done():
			return
		case m := <-st.out:
			ctx, cancel := context.WithTimeout(st.ctx, dialTimeout)
			err := st.conn.Write(ctx, websocket.MessageText, m.data)
			cancel()
			if m.written != nil {
				m.written <- err
			}
			if err != nil {
				s.fail(st, "Не удалось отправить звук в Gemini")
				return
			}
		}
	}
}

type transcription struct {
	Text string `json:"text"`
}
type serverEvent struct {
	SetupComplete *struct{}        `json:"setupComplete"`
	Error         *json.RawMessage `json:"error"`
	GoAway        *json.RawMessage `json:"goAway"`
	ServerContent *struct {
		Interim      *transcription `json:"interimInputTranscription"`
		Final        *transcription `json:"inputTranscription"`
		TurnComplete bool           `json:"turnComplete"`
	} `json:"serverContent"`
}

func (s *Session) read(st *stream) {
	defer s.wg.Done()
	defer st.cancel()
	n, rev, active := 0, 0, false
	update := func(kind, text string) {
		if !active {
			if text == "" {
				return
			}
			n++
			rev = 0
			active = true
			s.emit(stt.Update{StreamID: st.id, UtteranceID: fmt.Sprintf("%s:%d", st.id, n), Kind: stt.KindStarted})
		}
		rev++
		s.emit(stt.Update{StreamID: st.id, UtteranceID: fmt.Sprintf("%s:%d", st.id, n), Revision: rev, Kind: kind, Text: text})
		if kind == stt.KindFinal {
			active = false
		}
	}
	for {
		_, b, err := st.conn.Read(st.ctx)
		if err != nil {
			if st.ctx.Err() == nil {
				s.fail(st, "Соединение с Gemini прервано; переподключите распознавание")
			}
			return
		}
		var ev serverEvent
		if json.Unmarshal(b, &ev) != nil {
			s.fail(st, "Gemini прислал некорректный ответ")
			return
		}
		if ev.Error != nil {
			s.fail(st, "Gemini остановил распознавание: проверьте ключ, доступ к модели и квоту")
			return
		}
		if content := ev.ServerContent; content != nil {
			if content.Interim != nil && strings.TrimSpace(content.Interim.Text) != "" {
				s.mu.Lock()
				st.hasPartial = true
				s.mu.Unlock()
				update(stt.KindPartial, content.Interim.Text)
			}
			if content.Final != nil {
				s.mu.Lock()
				st.hasPartial = false
				s.mu.Unlock()
				update(stt.KindFinal, content.Final.Text)
			}
			if content.Final != nil || content.TurnComplete {
				select {
				case st.finals <- struct{}{}:
				default:
				}
			}
		}
		if ev.GoAway != nil {
			s.fail(st, "Gemini завершает сессию; остановите и снова включите прослушивание")
			return
		}
	}
}

var _ stt.SessionAPI = (*Session)(nil)
