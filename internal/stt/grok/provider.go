// Package grok is the cloud speech recognition provider over xAI's streaming STT WebSocket
// (wss://api.x.ai/v1/stt). Audio leaves the computer; the key comes from the secret store.
package grok

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/97Dmitry/solveo/internal/stt"
)

const (
	defaultURL       = "wss://api.x.ai/v1/stt"
	model            = "grok-voice-transcribe-2.0"
	endpointingMs    = "800"
	maxKeyTerms      = 100
	maxTermRunes     = 50
	dialTimeout      = 10 * time.Second
	defaultFlushWait = 3 * time.Second
)

var errRejected = errors.New("Ключ отклонён xAI: проверьте его в панели распознавания")

type Provider struct {
	keys func() (string, bool, error)
	// URL is the streaming endpoint; tests point it at a fake server.
	URL string
}

// New reads the API key through keys on every check and start, so a changed key applies at once.
func New(keys func() (string, bool, error)) *Provider { return &Provider{keys: keys, URL: defaultURL} }

func (p *Provider) Describe() stt.Description {
	return stt.Description{
		ID: "grok", Label: "Grok STT (облако xAI)", Local: false, Preparable: false, NeedsKey: true, KeyLabel: "API-ключ xAI", Vendor: "xAI",
		StartLabel: "Подключить", StopLabel: "Отключить", MaxPhrases: maxKeyTerms, MaxPhraseRunes: maxTermRunes,
	}
}

func (p *Provider) key() (string, error) {
	k, ok, err := p.keys()
	if err != nil {
		return "", err
	}
	if !ok || k == "" {
		return "", errors.New("Сначала задайте ключ xAI в панели распознавания")
	}
	return k, nil
}

// handshake opens and immediately drops a streaming connection: it proves the key and the network.
func (p *Provider) handshake(ctx context.Context, key string, query url.Values) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, p.URL+"?"+query.Encode(), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + key}},
	})
	if err != nil {
		if resp == nil {
			return nil, fmt.Errorf("нет доступа к api.x.ai: %v", err)
		}
		reason := responseReason(resp)
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return nil, errRejected
		case http.StatusForbidden:
			return nil, fmt.Errorf("xAI отказал в доступе (403): %s", reason)
		default:
			return nil, fmt.Errorf("xAI ответил %d: %s", resp.StatusCode, reason)
		}
	}
	return conn, nil
}

// responseReason extracts xAI's error text from a rejected handshake; request headers never appear in it.
func responseReason(resp *http.Response) string {
	if resp.Body == nil {
		return http.StatusText(resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	var parsed struct {
		Error   any    `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		switch e := parsed.Error.(type) {
		case string:
			return e
		case map[string]any:
			if m, ok := e["message"].(string); ok {
				return m
			}
		}
		if parsed.Message != "" {
			return parsed.Message
		}
	}
	text := strings.TrimSpace(string(body))
	if r := []rune(text); len(r) > 200 {
		text = string(r[:200]) + "…"
	}
	if text == "" {
		return http.StatusText(resp.StatusCode)
	}
	return text
}

func baseQuery(rate int) url.Values {
	return url.Values{"model": {model}, "encoding": {"pcm"}, "sample_rate": {fmt.Sprint(rate)}}
}

func (p *Provider) Status(ctx context.Context) []stt.Check {
	keyCheck := stt.Check{ID: "key", Label: "Ключ xAI", State: stt.StateOK, Message: "Ключ задан"}
	network := stt.Check{ID: "network", Label: "Подключение к xAI", State: stt.StateUnknown}
	k, ok, err := p.keys()
	switch {
	case err != nil:
		keyCheck.State, keyCheck.Message = stt.StateError, err.Error()
		return []stt.Check{keyCheck, network}
	case !ok || k == "":
		keyCheck.State, keyCheck.Message = stt.StateMissing, "Ключ xAI не задан: введите его в панели распознавания"
		return []stt.Check{keyCheck, network}
	}
	conn, err := p.handshake(ctx, k, baseQuery(16000))
	if err != nil {
		network.State, network.Message = stt.StateError, err.Error()
		return []stt.Check{keyCheck, network}
	}
	_ = conn.CloseNow()
	network.State, network.Message = stt.StateOK, "xAI принимает ключ"
	return []stt.Check{keyCheck, network}
}

// Prepare has nothing to install for a cloud provider.
func (p *Provider) Prepare(context.Context, func(string)) error { return nil }

func (p *Provider) Start(ctx context.Context, progress func(string)) (stt.SessionAPI, error) {
	k, err := p.key()
	if err != nil {
		return nil, err
	}
	progress("Проверка ключа xAI")
	conn, err := p.handshake(ctx, k, baseQuery(16000))
	if err != nil {
		return nil, err
	}
	_ = conn.CloseNow()
	return newSession(p, k), nil
}

// Stop has nothing to release: the controller closes the session.
func (p *Provider) Stop(context.Context) error { return nil }
