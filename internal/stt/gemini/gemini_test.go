package gemini

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/97Dmitry/solveo/internal/stt"
)

func fake(t *testing.T, handle func(context.Context, *websocket.Conn)) *Provider {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "test-secret" || r.URL.RawQuery != "" {
			t.Error("invalid authentication")
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		handle(r.Context(), c)
	}))
	t.Cleanup(server.Close)
	p := New(func() (string, bool, error) { return "test-secret", true, nil })
	p.URL = "ws" + strings.TrimPrefix(server.URL, "http")
	return p
}
func readJSON(ctx context.Context, c *websocket.Conn) map[string]any {
	_, b, err := c.Read(ctx)
	if err != nil {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}
func send(ctx context.Context, c *websocket.Conn, v any) {
	b, _ := json.Marshal(v)
	_ = c.Write(ctx, websocket.MessageText, b)
}
func setup(ctx context.Context, c *websocket.Conn) map[string]any {
	m := readJSON(ctx, c)
	send(ctx, c, map[string]any{"setupComplete": map[string]any{}})
	return m
}
func session(t *testing.T, p *Provider) stt.SessionAPI {
	t.Helper()
	s, err := p.Start(context.Background(), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func TestStreamingDictionaryPCMAndFinal(t *testing.T) {
	received := make(chan map[string]any, 4)
	p := fake(t, func(ctx context.Context, c *websocket.Conn) {
		m := setup(ctx, c)
		frame := readJSON(ctx, c)
		if frame == nil {
			return
		}
		received <- m
		received <- frame
		send(ctx, c, map[string]any{"serverContent": map[string]any{"interimInputTranscription": map[string]any{"text": "В нест"}}})
		send(ctx, c, map[string]any{"serverContent": map[string]any{"inputTranscription": map[string]any{"text": "В NestJS dependency injection"}}})
		for {
			m = readJSON(ctx, c)
			if m == nil {
				return
			}
			received <- m
			send(ctx, c, map[string]any{"serverContent": map[string]any{"turnComplete": true}})
		}
	})
	s := session(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Open(ctx, "mic", "mic", []string{"NestJS"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Push("mic", 48000, 2, []float32{1, -1, 0.5, 0.5}); err != nil {
		t.Fatal(err)
	}
	var a, b map[string]any
	select {
	case a = <-received:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case b = <-received:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	conf := a["setup"].(map[string]any)
	if conf["model"] != "models/gemini-3.5-transcribe-live" {
		t.Fatal(conf)
	}
	trans := conf["inputAudioTranscription"].(map[string]any)
	if trans["mode"] != "VERBATIM" || len(trans["languageCodes"].([]any)) != 0 || trans["customVocabulary"].([]any)[0] != "NestJS" {
		t.Fatal(trans)
	}
	audio := b["realtimeInput"].(map[string]any)["audio"].(map[string]any)
	pcm, err := base64.StdEncoding.DecodeString(audio["data"].(string))
	if err != nil || len(pcm) != 4 || pcm[0] != 0 || pcm[1] != 0 || audio["mimeType"] != "audio/pcm;rate=48000" {
		t.Fatal(audio, pcm, err)
	}
	var partial stt.Update
	for {
		select {
		case e := <-s.Events():
			if u, ok := e.(stt.Update); ok {
				if u.Kind == stt.KindPartial {
					partial = u
				}
				if u.Kind == stt.KindFinal {
					if u.Text != "В NestJS dependency injection" || partial.UtteranceID != u.UtteranceID || partial.Revision >= u.Revision {
						t.Fatal(partial, u)
					}
					goto flushed
				}
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
flushed:
	if err := s.Flush(ctx, "mic"); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-received:
		if m["realtimeInput"].(map[string]any)["audioStreamEnd"] != true {
			t.Fatal(m)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
func TestRejectedKeyNeverLeaks(t *testing.T) {
	p := fake(t, func(ctx context.Context, c *websocket.Conn) {
		_ = readJSON(ctx, c)
		send(ctx, c, map[string]any{"error": map[string]any{"message": "bad test-secret"}})
	})
	_, err := p.Start(context.Background(), func(string) {})
	if err == nil || strings.Contains(err.Error(), "test-secret") {
		t.Fatal(err)
	}
}
func TestMissingKey(t *testing.T) {
	p := New(func() (string, bool, error) { return "", false, nil })
	if stt.Ready(p.Status(context.Background())) {
		t.Fatal("ready without key")
	}
	if _, err := p.Start(context.Background(), nil); err == nil {
		t.Fatal("started without key")
	}
}

func TestStreamFailureIsIsolatedAndPartialIsNotFinal(t *testing.T) {
	p := fake(t, func(ctx context.Context, c *websocket.Conn) {
		m := setup(ctx, c)
		frame := readJSON(ctx, c)
		if frame == nil {
			return
		}
		terms := m["setup"].(map[string]any)["inputAudioTranscription"].(map[string]any)["customVocabulary"].([]any)
		if len(terms) > 0 && terms[0] == "broken" {
			send(ctx, c, map[string]any{"serverContent": map[string]any{"interimInputTranscription": map[string]any{"text": "черновик"}}})
			send(ctx, c, map[string]any{"error": map[string]any{"message": "test-secret"}})
			return
		}
		send(ctx, c, map[string]any{"serverContent": map[string]any{"inputTranscription": map[string]any{"text": "NestJS"}}})
		_ = readJSON(ctx, c)
	})
	s := session(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, id := range []string{"broken", "healthy"} {
		if err := s.Open(ctx, id, id, []string{id}); err != nil {
			t.Fatal(err)
		}
		if err := s.Push(id, 16000, 1, []float32{0}); err != nil {
			t.Fatal(err)
		}
	}
	failed, final := false, false
	for !failed || !final {
		select {
		case e := <-s.Events():
			switch v := e.(type) {
			case stt.StreamError:
				if v.StreamID != "broken" || strings.Contains(v.Message, "test-secret") {
					t.Fatal(v)
				}
				failed = true
			case stt.Update:
				if v.Kind == stt.KindFinal {
					if v.StreamID != "healthy" || v.Text != "NestJS" {
						t.Fatal(v)
					}
					final = true
				}
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if err := s.Flush(ctx, "broken"); err != nil {
		t.Fatal(err)
	}
}
func TestGoAwayReportsRestart(t *testing.T) {
	p := fake(t, func(ctx context.Context, c *websocket.Conn) {
		setup(ctx, c)
		if readJSON(ctx, c) == nil {
			return
		}
		send(ctx, c, map[string]any{"goAway": map[string]any{"timeLeft": "1s"}})
		_ = readJSON(ctx, c)
	})
	s := session(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Open(ctx, "mic", "mic", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Push("mic", 16000, 1, []float32{0}); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-s.Events():
		if _, ok := e.(stt.StreamError); !ok {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("GoAway ignored")
	}
}
func TestQueueAndClosedSession(t *testing.T) {
	p := New(func() (string, bool, error) { return "key", true, nil })
	s := newSession(p, "key")
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	// A ready stream with no network consumer models a blocked writer deterministically.
	s.streams["mic"] = &stream{id: "mic", ctx: ctx, cancel: cancel, ready: true, out: make(chan outbound, maxQueued)}
	for range maxQueued {
		if err := s.Push("mic", 16000, 1, []float32{0}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Push("mic", 16000, 1, []float32{0}); err != stt.ErrBackpressure {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Open(context.Background(), "new", "mic", nil); err == nil {
		t.Fatal("opened a closed session")
	}
	if _, ok := <-s.Events(); ok {
		t.Fatal("events not closed")
	}
}
func TestDictionaryAndInvalidPCM(t *testing.T) {
	s := newSession(New(nil), "key")
	defer func() { _ = s.Close() }()
	for _, terms := range [][]string{make([]string, maxPhrases+1), {strings.Repeat("я", 129)}, {"bad\x00term"}, {""}} {
		if err := s.Open(context.Background(), "bad", "mic", terms); err == nil {
			t.Fatal("accepted invalid dictionary")
		}
	}
	for _, f := range []struct {
		rate, channels int
		samples        []float32
	}{{16000, 0, nil}, {0, 1, nil}, {48000, 2, []float32{0}}} {
		if err := s.Push("mic", f.rate, f.channels, f.samples); err == nil {
			t.Fatal("accepted invalid PCM")
		}
	}
}
func TestCloseCancelsPendingSetup(t *testing.T) {
	entered := make(chan struct{}, 1)
	p := fake(t, func(ctx context.Context, c *websocket.Conn) {
		if readJSON(ctx, c) == nil {
			return
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		_ = readJSON(ctx, c)
	})
	s := newSession(p, "test-secret")
	defer func() { _ = s.Close() }()
	result := make(chan error, 1)
	go func() { result <- s.Open(context.Background(), "mic", "mic", nil) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("no setup")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("Open succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("Open stuck")
	}
}

func TestSetupRequiresConfirmation(t *testing.T) {
	for _, reply := range []string{`{}`, `{"setupComplete":null}`, `{"setupComplete":{},"error":{"message":"test-secret"}}`} {
		t.Run(reply, func(t *testing.T) {
			p := fake(t, func(ctx context.Context, c *websocket.Conn) {
				_ = readJSON(ctx, c)
				_ = c.Write(ctx, websocket.MessageText, []byte(reply))
				_ = readJSON(ctx, c)
			})
			s, err := p.Start(context.Background(), nil)
			if s != nil {
				_ = s.Close()
			}
			if err == nil || strings.Contains(err.Error(), "test-secret") {
				t.Fatal("accepted missing setup confirmation", err)
			}
		})
	}
}

func TestFlushReportsUnconfirmedPartial(t *testing.T) {
	p := fake(t, func(ctx context.Context, c *websocket.Conn) {
		setup(ctx, c)
		if readJSON(ctx, c) == nil {
			return
		}
		send(ctx, c, map[string]any{"serverContent": map[string]any{"interimInputTranscription": map[string]any{"text": "ещё не подтверждено"}}})
		for readJSON(ctx, c) != nil {
		}
	})
	s := session(t, p)
	if err := s.Open(context.Background(), "mic", "mic", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Push("mic", 16000, 1, []float32{0.1}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		select {
		case event := <-s.Events():
			if u, ok := event.(stt.Update); ok && u.Kind == stt.KindPartial {
				goto flush
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
flush:
	flushCtx, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	if err := s.Flush(flushCtx, "mic"); err == nil {
		t.Fatal("unconfirmed partial was silently accepted")
	}
}

func TestTurnCompleteDoesNotConfirmPartial(t *testing.T) {
	p := fake(t, func(ctx context.Context, c *websocket.Conn) {
		setup(ctx, c)
		if readJSON(ctx, c) == nil {
			return
		}
		send(ctx, c, map[string]any{"serverContent": map[string]any{"interimInputTranscription": map[string]any{"text": "ещё не подтверждено"}}})
		for readJSON(ctx, c) != nil {
			send(ctx, c, map[string]any{"serverContent": map[string]any{"turnComplete": true}})
		}
	})
	s := session(t, p)
	if err := s.Open(context.Background(), "mic", "mic", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Push("mic", 16000, 1, []float32{0.1}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		select {
		case event := <-s.Events():
			if u, ok := event.(stt.Update); ok && u.Kind == stt.KindPartial {
				goto flush
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
flush:
	flushCtx, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	if err := s.Flush(flushCtx, "mic"); err == nil {
		t.Fatal("unconfirmed partial was silently accepted")
	}
}

func TestFlushSilenceDoesNotRequireFinal(t *testing.T) {
	p := fake(t, func(ctx context.Context, c *websocket.Conn) {
		setup(ctx, c)
		for readJSON(ctx, c) != nil {
		}
	})
	s := session(t, p)
	if err := s.Open(context.Background(), "mic", "mic", nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := s.Flush(ctx, "mic"); err != nil {
		t.Fatalf("silence is not a lost utterance: %v", err)
	}
}
