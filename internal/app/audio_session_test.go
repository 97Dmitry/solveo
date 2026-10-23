package app

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/97Dmitry/solveo/internal/audio"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/settings"
	"github.com/97Dmitry/solveo/internal/stt"
)

// endpointRuntime starts a real protocol v1 session against a fake worker.
type endpointRuntime struct {
	fakeRuntime
	addr string
}

func (r *endpointRuntime) Start(ctx context.Context, _ func(string)) (stt.SessionAPI, error) {
	session, err := stt.Dial(ctx, r.addr, "t")
	if err != nil {
		return nil, err
	}
	return session, nil
}

// fakeWorker answers protocol v1 like solveo_stt: every first frame of a stream yields one final phrase.
func fakeWorker(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(1 << 20)
		ctx := r.Context()
		send := func(v any) {
			data, _ := json.Marshal(v)
			_ = c.Write(ctx, websocket.MessageText, data)
		}
		answered := map[string]bool{}
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageText {
				var m map[string]any
				_ = json.Unmarshal(data, &m)
				if m["type"] == "open" {
					send(map[string]any{"type": "opened", "stream_id": m["stream_id"]})
				}
				continue
			}
			n := binary.LittleEndian.Uint32(data)
			var h struct {
				StreamID string `json:"stream_id"`
			}
			_ = json.Unmarshal(data[4:4+n], &h)
			if !answered[h.StreamID] {
				answered[h.StreamID] = true
				send(map[string]any{"type": "update", "stream_id": h.StreamID, "utterance_id": h.StreamID + ":1", "revision": 1, "kind": "final", "text": "фраза " + h.StreamID[:4]})
			}
		}
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestRealSessionDeliversTranscript(t *testing.T) {
	s, e := history.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	capturer := newFakeCapturer()
	runtime := &endpointRuntime{fakeRuntime: fakeRuntime{checks: readyChecks()}, addr: fakeWorker(t)}
	c, e := New(Dependencies{
		Store: s, SettingsStore: settings.Open(s.Dir), Work: openWork(t, s),
		Provider: &fakeProvider{started: make(chan llm.Request, 5), finish: make(chan error, 5), store: s},
		Audio:    AudioDeps{Providers: []stt.Provider{runtime}, Capturer: capturer},
	})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	if err := c.StartAudioModel(); err != nil {
		t.Fatal(err)
	}
	waitAudio(t, c, "ready", func(v AudioView) bool { return v.Model == "ready" && v.Busy == "" })
	if err := c.StartListening(); err != nil {
		t.Fatal(err)
	}
	capturer.frame(audio.System)
	v := waitAudio(t, c, "transcript", func(v AudioView) bool { return len(v.Segments) == 1 })
	if !v.Segments[0].Final || v.Segments[0].Track != "system" || !strings.HasPrefix(v.Segments[0].Text, "фраза ") {
		t.Fatalf("segment %+v", v.Segments[0])
	}
}
