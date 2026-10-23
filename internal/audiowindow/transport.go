// Package audiowindow connects an auxiliary renderer to the parent controller.
package audiowindow

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	core "github.com/97Dmitry/solveo/internal/app"
)

const maxBody = 1 << 20

type Config struct {
	ParentWindow uint64 `json:"parent_window"`
	ParentPID    uint32 `json:"parent_pid"`
	URL          string `json:"url"`
	Token        string `json:"token"`
	X            int    `json:"x"`
	Y            int    `json:"y"`
}
type View struct {
	Question    core.QuestionView `json:"question"`
	Docked      bool              `json:"docked"`
	WindowError string            `json:"window_error"`
	Pinned      bool              `json:"pinned"`
	Audio       core.AudioView    `json:"audio"`
	ChatID      string            `json:"chat_id"`
	ChatTitle   string            `json:"chat_title"`
	CanSend     bool              `json:"can_send"`
	Disabled    bool              `json:"disabled"`
	Theme       string            `json:"theme"`
	UIScale     int               `json:"ui_scale"`
	ShowVersion uint64            `json:"show_version"`
}
type Command struct {
	Question *core.QuestionCommand `json:"question,omitempty"`
	Name     string                `json:"name"`
	Provider string                `json:"provider,omitempty"`
	Key      string                `json:"key,omitempty"`
	IDs      []string              `json:"ids,omitempty"`
	Text     string                `json:"text,omitempty"`
	ChatID   string                `json:"chat_id,omitempty"`
}
type Insert struct {
	ExpiresAt int64  `json:"expires_at"`
	ID        string `json:"id"`
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
}
type Server struct {
	config  Config
	http    *http.Server
	show    atomic.Uint64
	mu      sync.Mutex
	pending map[string]chan string
}

type HistoryReader interface {
	LoadChatAudioHistory(string) (core.AudioHistoryView, error)
}

func New(view func() View, execute func(context.Context, Command) error, readers ...HistoryReader) (*Server, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{config: Config{URL: "http://" + listener.Addr().String(), Token: hex.EncodeToString(token)}, pending: map[string]chan string{}}
	s.show.Store(1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.config.Token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			_ = json.NewEncoder(w).Encode(map[string]uint64{"show_version": s.show.Load()})
		case r.Method == http.MethodGet && len(readers) > 0 && strings.HasPrefix(r.URL.Path, "/chat-history/"):
			result, err := readers[0].LoadChatAudioHistory(strings.TrimPrefix(r.URL.Path, "/chat-history/"))
			if err != nil {
				http.Error(w, "history unavailable", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(result)
		case r.Method == http.MethodGet && r.URL.Path == "/state":
			v := view()
			v.ShowVersion = s.show.Load()
			_ = json.NewEncoder(w).Encode(v)
		case r.Method == http.MethodPost && r.URL.Path == "/command":
			var cmd Command
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&cmd) != nil {
				http.Error(w, "invalid command", http.StatusBadRequest)
				return
			}
			if !allowed(cmd.Name) {
				http.Error(w, "unknown command", http.StatusBadRequest)
				return
			}
			if len(cmd.IDs) > 300 {
				http.Error(w, "too many segments", http.StatusBadRequest)
				return
			}
			if err := execute(r.Context(), cmd); err != nil {
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"error": ""})
		default:
			http.NotFound(w, r)
		}
	})
	s.http = &http.Server{Handler: handler, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 15 * time.Second}
	go func() { _ = s.http.Serve(listener) }()
	return s, nil
}
func allowed(name string) bool {
	switch name {
	case "question", "refresh", "prepare", "startModel", "stopModel", "startListening", "stopListening", "clear", "send", "insert", "deleteChatHistory", "selectProvider", "setKey", "deleteKey":
		return true
	}
	return false
}
func (s *Server) Config() Config { return s.config }
func (s *Server) Show()          { s.show.Add(1) }
func (s *Server) Close()         { _ = s.http.Close() }
func (s *Server) RequestInsert(ctx context.Context, chatID, text string, emit func(Insert)) error {
	if ctx.Err() != nil {
		return errors.New("Вставка отменена")
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	id := hex.EncodeToString(raw)
	reply := make(chan string, 1)
	s.mu.Lock()
	if len(s.pending) >= 8 {
		s.mu.Unlock()
		return errors.New("Вставка уже ожидает подтверждения")
	}
	s.pending[id] = reply
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	emit(Insert{ID: id, ChatID: chatID, Text: text, ExpiresAt: deadline.UnixMilli()})
	select {
	case message := <-reply:
		if message != "" {
			return errors.New(message)
		}
		return nil
	case <-ctx.Done():
		return errors.New("Основное окно не подтвердило вставку; повторите действие")
	}
}
func (s *Server) CompleteInsert(id, message string) {
	s.mu.Lock()
	reply := s.pending[id]
	s.mu.Unlock()
	if reply != nil {
		select {
		case reply <- message:
		default:
		}
	}
}

type Client struct {
	config Config
	http   *http.Client
}

func NewClient(c Config) (*Client, error) {
	u, err := url.Parse(c.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || len(c.Token) != 64 {
		return nil, errors.New("Некорректный канал окна аудио")
	}
	if _, err := hex.DecodeString(c.Token); err != nil {
		return nil, errors.New("Некорректный канал окна аудио")
	}
	return &Client{config: c, http: &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}}, nil
}
func (c *Client) request(path string, cmd *Command, target any) error {
	var body io.Reader
	method := http.MethodGet
	if cmd != nil {
		data, err := json.Marshal(cmd)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
		method = http.MethodPost
	}
	req, err := http.NewRequest(method, c.config.URL+path, body)
	if err != nil {
		return errors.New("Не удалось обратиться к основному окну")
	}
	req.Header.Set("Authorization", "Bearer "+c.config.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("Связь с основным окном потеряна")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		if strings.HasPrefix(path, "/chat-history/") {
			return errors.New("Не удалось прочитать историю аудио: файл недоступен, повреждён или имеет неподдерживаемую версию")
		}
		return errors.New("Основное окно отклонило команду аудио")
	}
	limit := int64(4 * maxBody)
	if strings.HasPrefix(path, "/chat-history/") {
		limit = 64 << 20
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, limit)).Decode(target); err != nil {
		return errors.New("Некорректный ответ основного окна")
	}
	return nil
}
func (c *Client) State() (View, error) {
	var v View
	err := c.request("/state", nil, &v)
	return v, err
}
func (c *Client) Execute(cmd Command) error {
	var result struct {
		Error string `json:"error"`
	}
	if err := c.request("/command", &cmd, &result); err != nil {
		return err
	}
	if result.Error != "" {
		return errors.New(result.Error)
	}
	return nil
}

// ShowVersion doubles as a cheap parent heartbeat without cloning transcript state.
func (c *Client) ShowVersion() (uint64, error) {
	var state struct {
		Version uint64 `json:"show_version"`
	}
	err := c.request("/health", nil, &state)
	return state.Version, err
}

func (c *Client) ChatHistory(id string) (core.AudioHistoryView, error) {
	var view core.AudioHistoryView
	err := c.request("/chat-history/"+url.PathEscape(id), nil, &view)
	return view, err
}
