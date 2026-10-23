package stt

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const answerTimeout = 10 * time.Second

// Health reports whether the worker has loaded and warmed up its model.
func Health(ctx context.Context, addr, token string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/health", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	_ = resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusServiceUnavailable:
		return false, nil
	default:
		return false, fmt.Errorf("распознаватель ответил %s", resp.Status)
	}
}

// Session is one authenticated connection to the worker. Push never blocks the caller.
// Every outgoing message goes through one ordered queue, so Flush and CloseStream stay behind
// the frames queued before them.
type Session struct {
	conn   *websocket.Conn
	cancel context.CancelFunc
	out    chan outbound
	events chan any
	done   chan struct{}

	mu      sync.Mutex
	seqs    map[string]uint64
	waiters map[string]chan error
	err     error
}

// Dial connects to ws://addr/v1 with the bearer token.
func Dial(ctx context.Context, addr, token string) (*Session, error) {
	conn, _, err := websocket.Dial(ctx, "ws://"+addr+"/v1", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
	})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(maxServerMessage)
	runCtx, cancel := context.WithCancel(context.Background())
	s := &Session{
		conn:    conn,
		cancel:  cancel,
		out:     make(chan outbound, maxQueuedFrames+16), // room for control messages behind full audio
		events:  make(chan any, eventBuffer),
		done:    make(chan struct{}),
		seqs:    map[string]uint64{},
		waiters: map[string]chan error{},
	}
	go s.write(runCtx)
	go s.read(runCtx)
	return s, nil
}

// Events delivers Update and StreamError values, then a final error when the session ends, then closes.
func (s *Session) Events() <-chan any { return s.events }

type outbound struct {
	typ  websocket.MessageType
	data []byte
}

func (s *Session) write(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-s.out:
			if err := s.conn.Write(ctx, m.typ, m.data); err != nil {
				s.cancel()
				return
			}
		}
	}
}

// enqueueControl waits for queue space: control messages are never dropped.
func (s *Session) enqueueControl(ctx context.Context, msg map[string]any) error {
	data, _ := json.Marshal(msg)
	select {
	case s.out <- outbound{websocket.MessageText, data}:
		return nil
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.err
	case <-ctx.Done():
		return errors.New("распознаватель не принимает команды")
	}
}

func (s *Session) read(ctx context.Context) {
	var err error
	for {
		var data []byte
		if _, data, err = s.conn.Read(ctx); err != nil {
			break
		}
		var m serverMessage
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Type {
		case "update":
			s.events <- m.update()
		case "opened":
			s.answer("open:"+m.StreamID, nil)
		case "flushed":
			s.answer("flush:"+m.RequestID, nil)
		case "error":
			if !s.answer("open:"+m.StreamID, errors.New(m.Message)) {
				s.events <- StreamError{StreamID: m.StreamID, Message: m.Message}
			}
		}
	}
	s.mu.Lock()
	s.err = fmt.Errorf("связь с распознавателем потеряна: %w", err)
	s.mu.Unlock()
	s.cancel()
	s.events <- s.err
	close(s.events)
	close(s.done)
}

func (s *Session) answer(key string, err error) bool {
	s.mu.Lock()
	ch, ok := s.waiters[key]
	delete(s.waiters, key)
	s.mu.Unlock()
	if ok {
		ch <- err
	}
	return ok
}

func (s *Session) request(ctx context.Context, key string, msg map[string]any) error {
	ch := make(chan error, 1)
	s.mu.Lock()
	s.waiters[key] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.waiters, key)
		s.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, answerTimeout)
	defer cancel()
	if err := s.enqueueControl(ctx, msg); err != nil {
		return err
	}
	select {
	case err := <-ch:
		return err
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.err
	case <-ctx.Done():
		return errors.New("распознаватель не ответил вовремя")
	}
}

// Open starts a stream; phrases become the recognition context.
func (s *Session) Open(ctx context.Context, streamID, track string, phrases []string) error {
	if phrases == nil {
		phrases = []string{}
	}
	return s.request(ctx, "open:"+streamID, map[string]any{"type": "open", "stream_id": streamID, "track": track, "context": phrases})
}

// Flush finishes the active utterance of a stream; it returns after its final was delivered.
func (s *Session) Flush(ctx context.Context, streamID string) error {
	var id [8]byte
	_, _ = rand.Read(id[:])
	requestID := hex.EncodeToString(id[:])
	return s.request(ctx, "flush:"+requestID, map[string]any{"type": "flush", "stream_id": streamID, "request_id": requestID})
}

// CloseStream drops a stream without waiting for pending output.
func (s *Session) CloseStream(streamID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), answerTimeout)
	defer cancel()
	return s.enqueueControl(ctx, map[string]any{"type": "close", "stream_id": streamID})
}

// Push queues one frame of interleaved float32 samples; a full queue drops it with ErrBackpressure.
func (s *Session) Push(streamID string, rate, channels int, samples []float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq := s.seqs[streamID] + 1
	header, _ := json.Marshal(map[string]any{"stream_id": streamID, "seq": seq, "rate": rate, "channels": channels})
	msg := make([]byte, 4+len(header)+4*len(samples))
	binary.LittleEndian.PutUint32(msg, uint32(len(header)))
	copy(msg[4:], header)
	body := msg[4+len(header):]
	for i, v := range samples {
		binary.LittleEndian.PutUint32(body[4*i:], math.Float32bits(v))
	}
	if len(s.out) >= maxQueuedFrames {
		return ErrBackpressure
	}
	select {
	case s.out <- outbound{websocket.MessageBinary, msg}:
		s.seqs[streamID] = seq
		return nil
	default:
		return ErrBackpressure
	}
}

// Close ends the session at once (no close handshake: a stuck worker must not delay shutdown).
// Events closes after the reader stops; consumers drain it until then.
func (s *Session) Close() error {
	s.cancel()
	return s.conn.CloseNow()
}
