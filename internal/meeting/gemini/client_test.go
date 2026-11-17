package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/97Dmitry/solveo/internal/meeting"
)

func TestFileTranscriptionAndCleanup(t *testing.T) {
	var base string
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "secret" {
			t.Error("missing header key")
		}
		switch r.URL.Path {
		case "/upload/v1beta/files":
			w.Header().Set("X-Goog-Upload-URL", base+"/upload-data")
		case "/upload-data":
			_, _ = w.Write([]byte(`{"file":{"name":"files/abc","uri":"https://generativelanguage.googleapis.com/v1beta/files/abc","state":"ACTIVE"}}`))
		case "/v1beta/interactions":
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["store"] != false {
				t.Error("request not stateless")
			}
			_, _ = w.Write([]byte(`{"status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"Hello","annotations":[{"type":"word_info","text":"Hello","speaker":"spk_1","start_offset":"0.1s","end_offset":"0.4s"}]}]}]}`))
		case "/v1beta/files/abc":
			deleted = true
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base = server.URL
	path := filepath.Join(t.TempDir(), "sample.wav")
	if e := os.WriteFile(path, make([]byte, 44+32000), 0600); e != nil {
		t.Fatal(e)
	}
	client := New(func() (string, bool, error) { return "secret", true, nil })
	client.BaseURL = base
	words, e := client.Transcribe(context.Background(), meeting.AudioInput{Path: path, Duration: 1})
	if e != nil {
		t.Fatal(e)
	}
	if len(words) != 1 || words[0].Text != "Hello" || words[0].Start != .1 {
		t.Fatalf("%+v", words)
	}
	if e = client.Cleanup(context.Background()); e != nil || !deleted {
		t.Fatalf("cleanup %v %v", deleted, e)
	}
}
func TestInvalidWordTimestamps(t *testing.T) {
	if _, e := parseWords(interaction{Status: "completed", Steps: []step{{Type: "model_output", Content: []content{{Text: "text", Annotations: []annotation{{Type: "word_info", Text: "text", Start: "2s", End: "1s"}}}}}}}, 3); e == nil {
		t.Fatal("accepted backwards timestamp")
	}
}

func TestSummaryUsesTextAndValidatesEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid request")
		}
		if string(body["model"]) != `"gemini-3.8-flash"` || string(body["store"]) != "false" || body["response_format"] == nil {
			t.Error("summary not configured")
		}
		summary := `{"overview":"Проверить релиз","topics":[],"decisions":[],"tasks":[{"text":"Проверить","owner":"","deadline":"","sources":["seg"]}],"questions":[]}`
		response := interaction{Status: "completed", Steps: []step{{Type: "model_output", Content: []content{{Type: "text", Text: summary}}}}}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	c := New(func() (string, bool, error) { return "key", true, nil })
	c.BaseURL = server.URL
	out, e := c.Summarize(context.Background(), []meeting.Segment{{ID: "seg", Text: "Нужно проверить релиз"}})
	if e != nil || len(out.Tasks) != 1 {
		t.Fatalf("%+v %v", out, e)
	}
}
func TestCleanupRetriesPersistedFilesAndTreatsExpiryAsDeleted(t *testing.T) {
	status := http.StatusServiceUnavailable
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "private vendor body", status) }))
	defer server.Close()
	journal := filepath.Join(t.TempDir(), "google-files.json")
	c := New(func() (string, bool, error) { return "key", true, nil })
	c.BaseURL = server.URL
	c.JournalPath = journal
	if e := c.remember("files/old"); e != nil {
		t.Fatal(e)
	}
	if e := c.Cleanup(context.Background()); e == nil || strings.Contains(e.Error(), "private vendor body") {
		t.Fatalf("unsafe error %v", e)
	}
	status = http.StatusNotFound
	next := New(func() (string, bool, error) { return "key", true, nil })
	next.BaseURL = server.URL
	next.JournalPath = journal
	if e := next.Cleanup(context.Background()); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(journal)
	if e != nil || string(b) != "{}" {
		t.Fatalf("journal %s %v", b, e)
	}
}
