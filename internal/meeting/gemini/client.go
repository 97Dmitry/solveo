// Package gemini processes recorded meetings through Google Files and Interactions APIs.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

const apiURL = "https://generativelanguage.googleapis.com"

var fileID = regexp.MustCompile(`^files/[A-Za-z0-9_-]{1,128}$`)

type Client struct {
	keys        func() (string, bool, error)
	BaseURL     string
	JournalPath string
	http        *http.Client
	mu          sync.Mutex
	files       map[string]bool
	loaded      bool
}

func New(keys func() (string, bool, error)) *Client {
	return &Client{keys: keys, BaseURL: apiURL, http: &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}, files: map[string]bool{}}
}
func (c *Client) key() (string, error) {
	key, ok, e := c.keys()
	if e != nil || !ok || key == "" {
		return "", errors.New("Задайте API-ключ Gemini в настройках распознавания")
	}
	return key, nil
}
func (c *Client) request(ctx context.Context, method, path string, body io.Reader, headers map[string]string) ([]byte, http.Header, error) {
	key, e := c.key()
	if e != nil {
		return nil, nil, e
	}
	target := c.BaseURL + path
	if u, e := url.Parse(path); e == nil && u.IsAbs() {
		base, _ := url.Parse(c.BaseURL)
		if u.Scheme != base.Scheme || u.Host != base.Host || u.User != nil {
			return nil, nil, errors.New("Gemini вернул недопустимый адрес загрузки")
		}
		target = path
	}
	req, e := http.NewRequestWithContext(ctx, method, target, body)
	if e != nil {
		return nil, nil, errors.New("Некорректный запрос Gemini")
	}
	if f, ok := body.(*os.File); ok {
		info, e := f.Stat()
		if e != nil {
			return nil, nil, errors.New("Не удалось прочитать аудиофайл")
		}
		req.ContentLength = info.Size()
	}
	req.Header.Set("x-goog-api-key", key)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, e := c.http.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, errors.New("Не удалось обратиться к Gemini; проверьте сеть")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, resp.Header, retryableError(resp.StatusCode, resp.Header, body, time.Now())
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.Header, &httpError{status: resp.StatusCode}
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 16<<20+1))
	if e != nil || len(b) > 16<<20 {
		return nil, nil, errors.New("Ответ Gemini слишком большой или не прочитан")
	}
	return b, resp.Header, nil
}
func (c *Client) json(ctx context.Context, path string, body any, target any) error {
	b, e := json.Marshal(body)
	if e != nil {
		return e
	}
	data, _, e := c.request(ctx, http.MethodPost, path, bytes.NewReader(b), map[string]string{"Content-Type": "application/json"})
	if e != nil {
		return e
	}
	if json.Unmarshal(data, target) != nil {
		return errors.New("Некорректный ответ Gemini")
	}
	return nil
}
func (c *Client) journal() error {
	if c.JournalPath == "" {
		return nil
	}
	if e := os.MkdirAll(filepath.Dir(c.JournalPath), 0700); e != nil {
		return errors.New("Не удалось сохранить список временных файлов Google")
	}
	b, e := json.Marshal(c.files)
	if e != nil {
		return e
	}
	if e = os.WriteFile(c.JournalPath+".tmp", b, 0600); e != nil {
		return errors.New("Не удалось сохранить список временных файлов Google")
	}
	if e = os.Rename(c.JournalPath+".tmp", c.JournalPath); e != nil {
		return errors.New("Не удалось сохранить список временных файлов Google")
	}
	return nil
}
func (c *Client) loadJournal() error {
	if c.loaded {
		return nil
	}
	if c.JournalPath != "" {
		b, e := os.ReadFile(c.JournalPath)
		if e != nil && !os.IsNotExist(e) {
			return errors.New("Не удалось прочитать список временных файлов Google")
		}
		if e == nil {
			if len(b) > 1<<20 || json.Unmarshal(b, &c.files) != nil {
				return errors.New("Повреждён список временных файлов Google")
			}
			for id := range c.files {
				if !fileID.MatchString(id) {
					return errors.New("Повреждён список временных файлов Google")
				}
			}
		}
	}
	if c.files == nil {
		c.files = map[string]bool{}
	}
	c.loaded = true
	return nil
}
func (c *Client) remember(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.loadJournal(); e != nil {
		return e
	}
	c.files[id] = true
	return c.journal()
}
func (c *Client) Cleanup(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.loadJournal(); e != nil {
		return e
	}
	var result error
	for id := range c.files {
		if ctx.Err() != nil {
			if result == nil {
				result = ctx.Err()
			}
			break
		}
		_, headers, e := c.request(ctx, http.MethodDelete, "/v1beta/"+id, nil, nil)
		_ = headers
		if e != nil {
			// Expired files have already been removed by Google.
			var gone *httpError
			if errors.As(e, &gone) && (gone.status == 404 || gone.status == 410) {
				delete(c.files, id)
				continue
			}
			result = errors.Join(result, e)
			continue
		}
		delete(c.files, id)
	}
	return errors.Join(result, c.journal())
}

// httpError is intentionally not exposed to renderers.
type httpError struct{ status int }

func (e *httpError) Error() string {
	return fmt.Sprintf("Gemini: HTTP %d; проверьте ключ, квоту и доступ к модели", e.status)
}
