package grok

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/97Dmitry/solveo/internal/stt"
)

const key = "xai-test-secret-key"

// fakeXAI accepts /v1/stt like xAI: bearer auth, transcript.created first, then handle.
type fakeXAI struct {
	mu       sync.Mutex
	requests []*http.Request
	url      string
}

func newFakeXAI(t *testing.T, handle func(ctx context.Context, c *websocket.Conn)) *fakeXAI {
	t.Helper()
	f := &fakeXAI{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r)
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+key {
			http.Error(w, "invalid api key", http.StatusUnauthorized)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(1 << 20)
		send(r.Context(), c, map[string]any{"type": "transcript.created"})
		if handle != nil {
			handle(r.Context(), c)
		}
		_ = c.CloseNow()
	}))
	t.Cleanup(srv.Close)
	f.url = "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/stt"
	return f
}

func (f *fakeXAI) query(i int) url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[i].URL.Query()
}

func send(ctx context.Context, c *websocket.Conn, v any) {
	data, _ := json.Marshal(v)
	_ = c.Write(ctx, websocket.MessageText, data)
}

func partial(text string, isFinal, speechFinal bool) map[string]any {
	return map[string]any{"type": "transcript.partial", "text": text, "is_final": isFinal, "speech_final": speechFinal, "start": 0.5, "duration": 1.0}
}

// readFrames waits for n binary frames, answering nothing.
func readUntil(ctx context.Context, c *websocket.Conn, want func(websocket.MessageType, []byte) bool) {
	for {
		typ, data, err := c.Read(ctx)
		if err != nil || want(typ, data) {
			return
		}
	}
}

func keys(k string) func() (string, bool, error) {
	return func() (string, bool, error) { return k, k != "", nil }
}

func provider(f *fakeXAI, k string) *Provider {
	p := New(keys(k))
	p.URL = f.url
	return p
}

func start(t *testing.T, p *Provider) *Session {
	t.Helper()
	s, err := p.Start(context.Background(), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s.(*Session)
}

func next(t *testing.T, s *Session) any {
	t.Helper()
	select {
	case ev := <-s.Events():
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("no event")
		return nil
	}
}

func updates(t *testing.T, s *Session, n int) []stt.Update {
	t.Helper()
	var out []stt.Update
	for len(out) < n {
		ev := next(t, s)
		u, ok := ev.(stt.Update)
		if !ok {
			t.Fatalf("unexpected event %#v", ev)
		}
		out = append(out, u)
	}
	return out
}

func TestDescribesCloudProviderNeedingAKey(t *testing.T) {
	d := New(keys("")).Describe()
	if d.ID != "grok" || d.Local || d.Preparable || !d.NeedsKey || d.StartLabel != "Подключить" || d.MaxPhrases != 100 || d.MaxPhraseRunes != 50 {
		t.Fatalf("description %+v", d)
	}
}

func TestStatusWithoutKeyAndWithRejectedOrAcceptedKey(t *testing.T) {
	f := newFakeXAI(t, nil)
	checks := provider(f, "").Status(context.Background())
	if len(checks) != 2 || checks[0].State != stt.StateMissing || checks[1].State != stt.StateUnknown {
		t.Fatalf("without key %+v", checks)
	}
	checks = provider(f, "wrong-key").Status(context.Background())
	if checks[0].State != stt.StateOK || checks[1].State != stt.StateError || !strings.Contains(checks[1].Message, "Ключ отклонён") {
		t.Fatalf("rejected key %+v", checks)
	}
	if strings.Contains(checks[1].Message, "wrong-key") {
		t.Fatal("the key leaked into the status")
	}
	checks = provider(f, key).Status(context.Background())
	if !stt.Ready(checks) {
		t.Fatalf("accepted key %+v", checks)
	}
}

func TestStartRejectsMissingOrInvalidKey(t *testing.T) {
	f := newFakeXAI(t, nil)
	if _, err := provider(f, "").Start(context.Background(), func(string) {}); err == nil {
		t.Fatal("start without key")
	}
	_, err := provider(f, "bad-secret").Start(context.Background(), func(string) {})
	if err == nil || strings.Contains(err.Error(), "bad-secret") {
		t.Fatalf("start with a rejected key: %v", err)
	}
}

func TestDialSendsKeyAndQuery(t *testing.T) {
	f := newFakeXAI(t, func(ctx context.Context, c *websocket.Conn) {
		readUntil(ctx, c, func(websocket.MessageType, []byte) bool { return false })
	})
	s := start(t, provider(f, key))
	if err := s.Open(context.Background(), "a", "system", []string{"NestJS", "JWT"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Push("a", 48000, 2, make([]float32, 9600)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		n := len(f.requests)
		f.mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	q := f.query(1) // 0 is the key check in Start
	want := map[string]string{"model": "grok-voice-transcribe-2.0", "encoding": "pcm", "sample_rate": "48000", "interim_results": "true", "endpointing": "800"}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s=%q, want %q", k, q.Get(k), v)
		}
	}
	if strings.Join(q["keyterm"], ",") != "NestJS,JWT" {
		t.Fatalf("keyterms %v", q["keyterm"])
	}
	if q.Get("channels") != "" && q.Get("channels") != "1" {
		t.Fatal("stream must be mono")
	}
}

func TestDictionaryOverLimitFailsOpen(t *testing.T) {
	f := newFakeXAI(t, nil)
	s := start(t, provider(f, key))
	many := make([]string, 101)
	for i := range many {
		many[i] = "t" + strings.Repeat("x", i%3)
	}
	if err := s.Open(context.Background(), "a", "system", many); err == nil {
		t.Fatal("101 terms accepted")
	}
	if err := s.Open(context.Background(), "b", "system", []string{strings.Repeat("ж", 51)}); err == nil {
		t.Fatal("51-character term accepted")
	}
}

func TestFramesArePCM16MonoAtNativeRate(t *testing.T) {
	got := make(chan []byte, 1)
	f := newFakeXAI(t, func(ctx context.Context, c *websocket.Conn) {
		readUntil(ctx, c, func(typ websocket.MessageType, data []byte) bool {
			if typ == websocket.MessageBinary {
				got <- data
				return true
			}
			return false
		})
	})
	s := start(t, provider(f, key))
	_ = s.Open(context.Background(), "a", "mic", nil)
	if err := s.Push("a", 44100, 2, []float32{0.5, 0.5, -1, -0.5, 2, 2}); err != nil {
		t.Fatal(err)
	}
	data := <-got
	if len(data) != 6 {
		t.Fatalf("%d bytes for 3 mono samples", len(data))
	}
	vals := []int16{int16(binary.LittleEndian.Uint16(data)), int16(binary.LittleEndian.Uint16(data[2:])), int16(binary.LittleEndian.Uint16(data[4:]))}
	if vals[0] != 16384 || vals[1] != -24575 || vals[2] != 32767 { // round(v*32767), clamped
		t.Fatalf("samples %v", vals)
	}
	if f.query(1).Get("sample_rate") != "44100" {
		t.Fatal("rate not taken from the device")
	}
}

func TestUnsupportedRateFailsStream(t *testing.T) {
	f := newFakeXAI(t, nil)
	s := start(t, provider(f, key))
	_ = s.Open(context.Background(), "a", "system", nil)
	_ = s.Push("a", 96000, 2, make([]float32, 100))
	ev := next(t, s)
	if e, ok := ev.(stt.StreamError); !ok || e.StreamID != "a" || !strings.Contains(e.Message, "96000") {
		t.Fatalf("event %#v", ev)
	}
}

func TestInterimAndLockedChunksBuildPartials(t *testing.T) {
	f := newFakeXAI(t, func(ctx context.Context, c *websocket.Conn) {
		readUntil(ctx, c, func(typ websocket.MessageType, _ []byte) bool { return typ == websocket.MessageBinary })
		send(ctx, c, partial("Почему context", false, false))
		send(ctx, c, partial("Почему context cancellation", true, false))
		send(ctx, c, partial("не остановил", false, false))
		send(ctx, c, partial("не остановил goroutine?", true, true))
		readUntil(ctx, c, func(websocket.MessageType, []byte) bool { return false })
	})
	s := start(t, provider(f, key))
	_ = s.Open(context.Background(), "a", "system", nil)
	_ = s.Push("a", 16000, 1, make([]float32, 1600))
	got := updates(t, s, 5)
	kinds := []string{}
	for _, u := range got {
		kinds = append(kinds, u.Kind)
		if u.StreamID != "a" || u.UtteranceID != "a:1" {
			t.Fatalf("update ids %+v", u)
		}
	}
	if strings.Join(kinds, ",") != "started,partial,partial,partial,final" {
		t.Fatalf("kinds %v", kinds)
	}
	if got[3].Text != "Почему context cancellation не остановил" || got[4].Text != "Почему context cancellation не остановил goroutine?" {
		t.Fatalf("texts %q / %q", got[3].Text, got[4].Text)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Revision <= got[i-1].Revision {
			t.Fatal("revisions must grow")
		}
	}
}

func TestFinalWithAndWithoutLockedPrefix(t *testing.T) {
	f := newFakeXAI(t, func(ctx context.Context, c *websocket.Conn) {
		readUntil(ctx, c, func(typ websocket.MessageType, _ []byte) bool { return typ == websocket.MessageBinary })
		send(ctx, c, partial("Первая часть", true, false))
		send(ctx, c, partial("Первая часть и вторая", true, true)) // cumulative final
		send(ctx, c, partial("Снова", true, false))
		send(ctx, c, partial("отдельно", true, true)) // tail-only final
		readUntil(ctx, c, func(websocket.MessageType, []byte) bool { return false })
	})
	s := start(t, provider(f, key))
	_ = s.Open(context.Background(), "a", "system", nil)
	_ = s.Push("a", 16000, 1, make([]float32, 1600))
	var finals []string
	for len(finals) < 2 {
		if u := updates(t, s, 1)[0]; u.Kind == stt.KindFinal {
			finals = append(finals, u.Text)
		}
	}
	if finals[0] != "Первая часть и вторая" || finals[1] != "Снова отдельно" {
		t.Fatalf("finals %q", finals)
	}
}

func TestFlushSendsFinalizeAndWaits(t *testing.T) {
	f := newFakeXAI(t, func(ctx context.Context, c *websocket.Conn) {
		readUntil(ctx, c, func(typ websocket.MessageType, data []byte) bool {
			if typ == websocket.MessageText && strings.Contains(string(data), "Finalize") {
				send(ctx, c, partial("хвост фразы", true, true))
			}
			return false
		})
	})
	s := start(t, provider(f, key))
	_ = s.Open(context.Background(), "a", "system", nil)
	_ = s.Push("a", 16000, 1, make([]float32, 1600))
	if err := s.Flush(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	var final string
	for final == "" {
		if u := updates(t, s, 1)[0]; u.Kind == stt.KindFinal {
			final = u.Text
		}
	}
	if final != "хвост фразы" {
		t.Fatalf("final %q", final)
	}
}

func TestIdleFlushTimesOut(t *testing.T) {
	f := newFakeXAI(t, func(ctx context.Context, c *websocket.Conn) {
		readUntil(ctx, c, func(websocket.MessageType, []byte) bool { return false })
	})
	s := start(t, provider(f, key))
	s.flushWait = 50 * time.Millisecond
	_ = s.Open(context.Background(), "a", "system", nil)
	_ = s.Push("a", 16000, 1, make([]float32, 1600))
	started := time.Now()
	if err := s.Flush(context.Background(), "a"); err != nil {
		t.Fatalf("idle flush must succeed: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("idle flush waited too long")
	}
}

func TestServerCloseIsStreamError(t *testing.T) {
	f := newFakeXAI(t, func(ctx context.Context, c *websocket.Conn) {
		readUntil(ctx, c, func(typ websocket.MessageType, _ []byte) bool { return typ == websocket.MessageBinary })
		send(ctx, c, map[string]any{"type": "error", "message": "stream timeout"})
	})
	s := start(t, provider(f, key))
	_ = s.Open(context.Background(), "a", "system", nil)
	_ = s.Push("a", 16000, 1, make([]float32, 1600))
	ev := next(t, s)
	if e, ok := ev.(stt.StreamError); !ok || e.StreamID != "a" || !strings.Contains(e.Message, "stream timeout") {
		t.Fatalf("event %#v", ev)
	}
	select {
	case ev := <-s.Events():
		if _, isErr := ev.(error); isErr {
			t.Fatal("a stream failure must not end the session")
		}
	case <-time.After(100 * time.Millisecond):
	}
}

func TestDialErrorHidesKey(t *testing.T) {
	f := newFakeXAI(t, nil)
	s := start(t, provider(f, key))
	s.key = "rotated-secret" // the key was replaced after start: the stream dial is rejected
	_ = s.Open(context.Background(), "a", "system", nil)
	_ = s.Push("a", 16000, 1, make([]float32, 1600))
	ev := next(t, s)
	e, ok := ev.(stt.StreamError)
	if !ok || strings.Contains(e.Message, "rotated-secret") || !strings.Contains(e.Message, "Ключ отклонён") {
		t.Fatalf("event %#v", ev)
	}
}

func TestBackpressure(t *testing.T) {
	block := make(chan struct{})
	f := newFakeXAI(t, func(ctx context.Context, c *websocket.Conn) { <-block })
	defer close(block)
	s := start(t, provider(f, key))
	_ = s.Open(context.Background(), "a", "system", nil)
	big := make([]float32, 9600)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := s.Push("a", 48000, 2, big); errors.Is(err, stt.ErrBackpressure) {
			return
		}
	}
	t.Fatal("no back-pressure")
}

func TestNonFatalErrorKeepsStream(t *testing.T) {
	f := newFakeXAI(t, func(ctx context.Context, c *websocket.Conn) {
		readUntil(ctx, c, func(typ websocket.MessageType, _ []byte) bool { return typ == websocket.MessageBinary })
		send(ctx, c, map[string]any{"type": "error", "message": "could not parse message"})
		send(ctx, c, partial("всё ещё работает", true, true))
		readUntil(ctx, c, func(websocket.MessageType, []byte) bool { return false })
	})
	s := start(t, provider(f, key))
	_ = s.Open(context.Background(), "a", "system", nil)
	_ = s.Push("a", 16000, 1, make([]float32, 1600))
	for {
		ev := next(t, s)
		if e, ok := ev.(stt.StreamError); ok {
			t.Fatalf("a parse error ended the stream: %+v", e)
		}
		if u := ev.(stt.Update); u.Kind == stt.KindFinal {
			if u.Text != "всё ещё работает" {
				t.Fatalf("final %q", u.Text)
			}
			return
		}
	}
}

func TestUnfinishedUtteranceIsFinalizedWhenTheStreamEnds(t *testing.T) {
	f := newFakeXAI(t, func(ctx context.Context, c *websocket.Conn) {
		readUntil(ctx, c, func(typ websocket.MessageType, _ []byte) bool { return typ == websocket.MessageBinary })
		send(ctx, c, partial("Почему context", true, false))
		send(ctx, c, partial("не остановил", false, false))
	}) // the server drops the connection mid-utterance
	s := start(t, provider(f, key))
	_ = s.Open(context.Background(), "a", "system", nil)
	_ = s.Push("a", 16000, 1, make([]float32, 1600))
	var final string
	for final == "" {
		ev := next(t, s)
		if u, ok := ev.(stt.Update); ok && u.Kind == stt.KindFinal {
			final = u.Text
		}
		if _, ok := ev.(stt.StreamError); ok && final == "" {
			t.Fatal("stream error arrived before the pending phrase was finalized")
		}
	}
	if final != "Почему context не остановил" {
		t.Fatalf("final %q", final)
	}
}

func TestTranscriptDoneFinalizesPending(t *testing.T) {
	f := newFakeXAI(t, func(ctx context.Context, c *websocket.Conn) {
		readUntil(ctx, c, func(typ websocket.MessageType, _ []byte) bool { return typ == websocket.MessageBinary })
		send(ctx, c, partial("последняя фраза", true, false))
		readUntil(ctx, c, func(typ websocket.MessageType, data []byte) bool {
			return typ == websocket.MessageText && strings.Contains(string(data), "audio.done")
		})
		send(ctx, c, map[string]any{"type": "transcript.done", "text": "последняя фраза", "duration": 1.2})
	})
	s := start(t, provider(f, key))
	_ = s.Open(context.Background(), "a", "system", nil)
	_ = s.Push("a", 16000, 1, make([]float32, 1600))
	updates(t, s, 2) // started, partial
	if err := s.CloseStream("a"); err != nil {
		t.Fatal(err)
	}
	u := updates(t, s, 1)[0]
	if u.Kind != stt.KindFinal || u.Text != "последняя фраза" {
		t.Fatalf("update %+v", u)
	}
}

func TestAssemblerComparesWordsNotBytes(t *testing.T) {
	cases := []struct {
		locked, final, want string
	}{
		{"so the answer is", "So the answer is 42.", "So the answer is 42."},
		{"привет", "приветствую всех", "привет приветствую всех"},
		{"Почему context,", "почему context cancellation?", "почему context cancellation?"},
	}
	for _, tc := range cases {
		a := &assembler{stream: "a"}
		a.apply(xaiEvent{Text: tc.locked, IsFinal: true})
		got := a.apply(xaiEvent{Text: tc.final, IsFinal: true, SpeechFinal: true})
		if text := got[len(got)-1].Text; text != tc.want {
			t.Errorf("%q + %q = %q, want %q", tc.locked, tc.final, text, tc.want)
		}
	}
}

func TestHandshakeErrorsShowStatusAndReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limit exceeded for team"}`))
	}))
	defer srv.Close()
	p := New(keys(key))
	p.URL = "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/stt"
	checks := p.Status(context.Background())
	if msg := checks[1].Message; !strings.Contains(msg, "429") || !strings.Contains(msg, "rate limit exceeded for team") || strings.Contains(msg, key) {
		t.Fatalf("message %q", msg)
	}
}

func TestErrorInsteadOfSessionStartIsShown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		if r.URL.Query().Get("sample_rate") == "22050" {
			send(r.Context(), c, map[string]any{"type": "error", "message": "unsupported sample rate"})
		} else {
			send(r.Context(), c, map[string]any{"type": "transcript.created"})
		}
		_ = c.CloseNow()
	}))
	defer srv.Close()
	p := New(keys(key))
	p.URL = "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/stt"
	s := start(t, p)
	_ = s.Open(context.Background(), "a", "system", nil)
	_ = s.Push("a", 22050, 1, make([]float32, 100))
	ev := next(t, s)
	if e, ok := ev.(stt.StreamError); !ok || !strings.Contains(e.Message, "unsupported sample rate") {
		t.Fatalf("event %#v", ev)
	}
}
