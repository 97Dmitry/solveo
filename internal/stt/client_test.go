package stt

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const token = "secret-token"

// fakeWorker runs handle for every accepted /v1 session; /health answers ready.
func fakeWorker(t *testing.T, ready bool, handle func(ctx context.Context, c *websocket.Conn)) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/health" {
			if !ready {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"ready":true}`))
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(1 << 20)
		handle(r.Context(), c)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func readJSON(ctx context.Context, c *websocket.Conn) map[string]any {
	_, data, err := c.Read(ctx)
	if err != nil {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}

func writeJSON(ctx context.Context, c *websocket.Conn, v any) {
	data, _ := json.Marshal(v)
	_ = c.Write(ctx, websocket.MessageText, data)
}

func dial(t *testing.T, addr string) *Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := Dial(ctx, addr, token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestHealthReadyAndNotReady(t *testing.T) {
	ctx := context.Background()
	ok, err := Health(ctx, fakeWorker(t, true, nil), token)
	if err != nil || !ok {
		t.Fatalf("ready worker: %v %v", ok, err)
	}
	ok, err = Health(ctx, fakeWorker(t, false, nil), token)
	if err != nil || ok {
		t.Fatalf("loading worker: %v %v", ok, err)
	}
	if _, err = Health(ctx, fakeWorker(t, true, nil), "wrong"); err == nil {
		t.Fatal("bad token must be an error")
	}
}

func TestDialSendsBearerToken(t *testing.T) {
	addr := fakeWorker(t, true, func(ctx context.Context, c *websocket.Conn) { <-ctx.Done() })
	if _, err := Dial(context.Background(), addr, "wrong"); err == nil {
		t.Fatal("dial with a wrong token must fail")
	}
	dial(t, addr)
}

func TestOpenWaitsForOpenedOrError(t *testing.T) {
	addr := fakeWorker(t, true, func(ctx context.Context, c *websocket.Conn) {
		for {
			m := readJSON(ctx, c)
			if m == nil {
				return
			}
			if m["stream_id"] == "bad" {
				writeJSON(ctx, c, map[string]any{"type": "error", "stream_id": "bad", "message": "словарь слишком большой"})
			} else {
				writeJSON(ctx, c, map[string]any{"type": "opened", "stream_id": m["stream_id"]})
			}
		}
	})
	s := dial(t, addr)
	ctx := context.Background()
	if err := s.Open(ctx, "a", "system", []string{"JWT"}); err != nil {
		t.Fatal(err)
	}
	err := s.Open(ctx, "bad", "mic", nil)
	if err == nil || !strings.Contains(err.Error(), "словарь слишком большой") {
		t.Fatalf("expected worker error, got %v", err)
	}
}

func TestPushEncodesHeaderAndSamples(t *testing.T) {
	got := make(chan []byte, 3)
	addr := fakeWorker(t, true, func(ctx context.Context, c *websocket.Conn) {
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				got <- data
			}
		}
	})
	s := dial(t, addr)
	for range 3 {
		if err := s.Push("a", 48000, 2, []float32{0.5, -0.25}); err != nil {
			t.Fatal(err)
		}
	}
	for want := 1; want <= 3; want++ {
		data := <-got
		n := binary.LittleEndian.Uint32(data)
		var h struct {
			StreamID string `json:"stream_id"`
			Seq      int    `json:"seq"`
			Rate     int    `json:"rate"`
			Channels int    `json:"channels"`
		}
		if err := json.Unmarshal(data[4:4+n], &h); err != nil {
			t.Fatal(err)
		}
		if h.StreamID != "a" || h.Seq != want || h.Rate != 48000 || h.Channels != 2 {
			t.Fatalf("header %+v", h)
		}
		body := data[4+n:]
		if math.Float32frombits(binary.LittleEndian.Uint32(body)) != 0.5 || math.Float32frombits(binary.LittleEndian.Uint32(body[4:])) != -0.25 {
			t.Fatal("samples are not float32 little endian")
		}
	}
}

func TestPushBackpressure(t *testing.T) {
	block := make(chan struct{})
	addr := fakeWorker(t, true, func(ctx context.Context, c *websocket.Conn) {
		<-block // never reads: the socket buffers fill and the writer stalls
		<-ctx.Done()
	})
	defer close(block)
	s := dial(t, addr)
	big := make([]float32, 9600)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := s.Push("a", 48000, 2, big); errors.Is(err, ErrBackpressure) {
			return
		}
	}
	t.Fatal("Push never reported back-pressure")
}

func TestDroppedFrameDoesNotAdvanceSequence(t *testing.T) {
	s := &Session{out: make(chan outbound), seqs: map[string]uint64{}}
	if err := s.Push("a", 16000, 1, []float32{0}); !errors.Is(err, ErrBackpressure) {
		t.Fatal("expected back-pressure on a full queue")
	}
	if s.seqs["a"] != 0 {
		t.Fatal("a dropped frame consumed a sequence number")
	}
}

func TestFlushWaitsForFlushed(t *testing.T) {
	addr := fakeWorker(t, true, func(ctx context.Context, c *websocket.Conn) {
		m := readJSON(ctx, c)
		writeJSON(ctx, c, map[string]any{"type": "update", "stream_id": "a", "utterance_id": "a:1", "revision": 1, "kind": "final", "text": "готово"})
		writeJSON(ctx, c, map[string]any{"type": "flushed", "stream_id": "a", "request_id": m["request_id"]})
		<-ctx.Done()
	})
	s := dial(t, addr)
	if err := s.Flush(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	ev := <-s.Events()
	u, ok := ev.(Update)
	if !ok || u.Kind != KindFinal || u.Text != "готово" || u.UtteranceID != "a:1" || u.StreamID != "a" {
		t.Fatalf("unexpected event %#v", ev)
	}
}

func TestFlushDeadline(t *testing.T) {
	addr := fakeWorker(t, true, func(ctx context.Context, c *websocket.Conn) { <-ctx.Done() })
	s := dial(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.Flush(ctx, "a"); err == nil {
		t.Fatal("flush without an answer must time out")
	}
}

func TestStreamErrorIsDelivered(t *testing.T) {
	addr := fakeWorker(t, true, func(ctx context.Context, c *websocket.Conn) {
		writeJSON(ctx, c, map[string]any{"type": "error", "stream_id": "a", "message": "пропуск"})
		writeJSON(ctx, c, map[string]any{"type": "something-new"})
		<-ctx.Done()
	})
	s := dial(t, addr)
	ev := <-s.Events()
	if e, ok := ev.(StreamError); !ok || e.StreamID != "a" || e.Message != "пропуск" {
		t.Fatalf("unexpected event %#v", ev)
	}
}

func TestServerCloseDeliversErrorThenCloses(t *testing.T) {
	addr := fakeWorker(t, true, func(ctx context.Context, c *websocket.Conn) {
		_ = c.Close(websocket.StatusInternalError, "model crashed")
	})
	s := dial(t, addr)
	var last any
	for ev := range s.Events() {
		last = ev
	}
	if _, ok := last.(error); !ok {
		t.Fatalf("expected a session error before close, got %#v", last)
	}
}

func TestOversizedServerMessageIsSessionError(t *testing.T) {
	addr := fakeWorker(t, true, func(ctx context.Context, c *websocket.Conn) {
		writeJSON(ctx, c, map[string]any{"type": "update", "text": strings.Repeat("x", 300<<10)})
		<-ctx.Done()
	})
	s := dial(t, addr)
	var last any
	for ev := range s.Events() {
		last = ev
	}
	if _, ok := last.(error); !ok {
		t.Fatalf("expected a session error, got %#v", last)
	}
}

func TestControlMessagesStayBehindQueuedFrames(t *testing.T) {
	order := make(chan string, 200)
	addr := fakeWorker(t, true, func(ctx context.Context, c *websocket.Conn) {
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				order <- "frame"
				continue
			}
			var m map[string]any
			_ = json.Unmarshal(data, &m)
			order <- m["type"].(string)
			if m["type"] == "flush" {
				writeJSON(ctx, c, map[string]any{"type": "flushed", "stream_id": "a", "request_id": m["request_id"]})
			}
		}
	})
	s := dial(t, addr)
	big := make([]float32, 9600)
	pushed := 0
	for range 40 {
		if s.Push("a", 48000, 2, big) == nil {
			pushed++
		}
	}
	if err := s.Flush(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseStream("a"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < pushed; i++ {
		if got := <-order; got != "frame" {
			t.Fatalf("message %d was %q before all %d queued frames", i, got, pushed)
		}
	}
	if got := <-order; got != "flush" {
		t.Fatalf("expected flush after the frames, got %q", got)
	}
	if got := <-order; got != "close" {
		t.Fatalf("expected close last, got %q", got)
	}
}
