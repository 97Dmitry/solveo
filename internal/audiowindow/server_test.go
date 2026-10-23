package audiowindow

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	core "github.com/97Dmitry/solveo/internal/app"
)

func TestTransportRejectsUnauthenticatedAndBrowserRequests(t *testing.T) {
	s, err := New(func() View { return View{ChatID: "chat"} }, func(context.Context, Command) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, origin := range []string{"", "https://example.com"} {
		req, _ := http.NewRequest(http.MethodGet, s.Config().URL+"/state", nil)
		if origin != "" {
			req.Header.Set("Authorization", "Bearer "+s.Config().Token)
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatal(resp.StatusCode)
		}
	}
	c, err := NewClient(s.Config())
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.State()
	if err != nil || v.ChatID != "chat" {
		t.Fatal(v, err)
	}
	if err := c.Execute(Command{Name: "deleteChat"}); err == nil {
		t.Fatal("unknown command accepted")
	}
}

func TestClientRejectsNonLoopbackAndSecretsInErrors(t *testing.T) {
	for _, url := range []string{"https://example.com", "http://localhost:1234", "http://127.0.0.1:1234/path", "http://secret@127.0.0.1:1234"} {
		if _, err := NewClient(Config{URL: url, Token: strings.Repeat("a", 64)}); err == nil {
			t.Fatal("unsafe endpoint accepted")
		}
	}
}

func TestInsertAcknowledgementAndCancellation(t *testing.T) {
	s, err := New(func() View { return View{} }, func(context.Context, Command) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.RequestInsert(context.Background(), "chat", "draft", func(v Insert) {
		if v.ChatID != "chat" || v.Text != "draft" || v.ExpiresAt == 0 {
			t.Error("invalid insertion")
		}
		s.CompleteInsert(v.ID, "")
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.RequestInsert(ctx, "chat", "draft", func(Insert) {}); err == nil {
		t.Fatal("canceled insert accepted")
	}
	s.mu.Lock()
	count := len(s.pending)
	s.mu.Unlock()
	if count != 0 {
		t.Fatal("pending insertion leaked")
	}
}

func TestCommandDispatchAndShowVersion(t *testing.T) {
	calls := 0
	s, err := New(func() View { return View{ChatID: "chat"} }, func(_ context.Context, c Command) error {
		if c.Name == "send" && c.ChatID == "chat" && len(c.IDs) == 1 {
			calls++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := NewClient(s.Config())
	if err != nil {
		t.Fatal(err)
	}
	before, err := c.State()
	if err != nil {
		t.Fatal(err)
	}
	s.Show()
	version, err := c.ShowVersion()
	if err != nil || version <= before.ShowVersion {
		t.Fatal("native reveal intent lost", err)
	}
	after, err := c.State()
	if err != nil || after.ShowVersion <= before.ShowVersion {
		t.Fatal("show intent lost", err)
	}
	if err := c.Execute(Command{Name: "send", ChatID: "chat", IDs: []string{"segment"}}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("command not delivered")
	}
	if err := c.Execute(Command{Name: "send", IDs: make([]string, 301)}); err == nil {
		t.Fatal("oversized selection accepted")
	}
}

type fakeAudioHistoryReader struct{}

func (fakeAudioHistoryReader) LoadChatAudioHistory(id string) (core.AudioHistoryView, error) {
	if id != "11111111111111111111111111111111" {
		return core.AudioHistoryView{}, errors.New("unknown chat")
	}
	return core.AudioHistoryView{ID: id, ChatID: id, Segments: []core.TranscriptSegment{{ID: "final", Text: "сохранено", Final: true}}}, nil
}
func TestHistoryRoutesReturnOnlyExplicitChatRequests(t *testing.T) {
	s, err := New(func() View { return View{} }, func(context.Context, Command) error { return nil }, fakeAudioHistoryReader{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := NewClient(s.Config())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := c.ChatHistory("11111111111111111111111111111111")
	if err != nil || len(doc.Segments) != 1 || doc.Segments[0].Text != "сохранено" {
		t.Fatal(doc, err)
	}
	if _, err := c.ChatHistory("../../settings"); err == nil {
		t.Fatal("invalid chat accepted")
	}
	for _, path := range []string{"/history", "/history/11111111111111111111111111111111"} {
		if err := c.request(path, nil, new(any)); err == nil {
			t.Fatal("legacy endpoint accepted", path)
		}
	}
	for _, name := range []string{"attachHistory", "deleteHistory"} {
		if err := c.Execute(Command{Name: name}); err == nil {
			t.Fatal("legacy command accepted", name)
		}
	}
}
