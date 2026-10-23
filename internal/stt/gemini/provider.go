// Package gemini implements cloud speech recognition using Gemini Transcribe Live.
package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/97Dmitry/solveo/internal/stt"
)

const (
	defaultURL     = "wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"
	model          = "models/gemini-3.5-transcribe-live"
	dialTimeout    = 10 * time.Second
	maxPhrases     = 256 // Application dictionary limit; the API permits 1000.
	maxPhraseRunes = 128
)

type Provider struct {
	keys func() (string, bool, error)
	// URL allows tests to substitute an in-process WebSocket server.
	URL string
}

func New(keys func() (string, bool, error)) *Provider { return &Provider{keys: keys, URL: defaultURL} }
func (*Provider) Describe() stt.Description {
	return stt.Description{
		ID: "gemini", Label: "Gemini 3.5 Transcribe Live (облако Google)", NeedsKey: true, Vendor: "Google", KeyLabel: "API-ключ Gemini",
		StartLabel: "Подключить", StopLabel: "Отключить", MaxPhrases: maxPhrases, MaxPhraseRunes: maxPhraseRunes,
	}
}
func (p *Provider) key() (string, error) {
	k, ok, err := p.keys()
	if err != nil {
		return "", errors.New("Не удалось прочитать ключ Gemini из хранилища")
	}
	if !ok || strings.TrimSpace(k) == "" {
		return "", errors.New("Сначала задайте API-ключ Gemini в панели распознавания")
	}
	return k, nil
}
func (p *Provider) Status(ctx context.Context) []stt.Check {
	checks := []stt.Check{{ID: "key", Label: "Ключ Gemini", State: stt.StateOK, Message: "Ключ задан"}, {ID: "network", Label: "Доступ к Gemini Transcribe Live", State: stt.StateUnknown}}
	key, err := p.key()
	if err != nil {
		checks[0].State, checks[0].Message = stt.StateMissing, err.Error()
		return checks
	}
	c, err := p.connect(ctx, key, nil)
	if err != nil {
		checks[1].State, checks[1].Message = stt.StateError, err.Error()
		return checks
	}
	_ = c.CloseNow()
	checks[1].State, checks[1].Message = stt.StateOK, "Gemini принимает ключ и модель доступна"
	return checks
}
func (*Provider) Prepare(context.Context, func(string)) error { return nil }
func (p *Provider) Start(ctx context.Context, progress func(string)) (stt.SessionAPI, error) {
	key, err := p.key()
	if err != nil {
		return nil, err
	}
	if progress != nil {
		progress("Проверка доступа к Gemini Transcribe Live")
	}
	c, err := p.connect(ctx, key, nil)
	if err != nil {
		return nil, err
	}
	_ = c.CloseNow()
	return newSession(p, key), nil
}
func (*Provider) Stop(context.Context) error { return nil }

// connect verifies setupComplete, not just the HTTP upgrade: model access is checked by setup.
// Only the authentication header contains the key; vendor errors are not reflected verbatim.
func (p *Provider) connect(ctx context.Context, key string, phrases []string) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, p.URL, &websocket.DialOptions{HTTPHeader: http.Header{"X-Goog-Api-Key": {key}}})
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("Подключение к Gemini: %w", ctx.Err())
		}
		if resp != nil {
			return nil, fmt.Errorf("Gemini отклонил подключение (HTTP %d): проверьте ключ, доступность API и регион", resp.StatusCode)
		}
		return nil, errors.New("Не удалось подключиться к Gemini: проверьте сеть и доступность API")
	}
	c.SetReadLimit(256 << 10)
	if phrases == nil {
		phrases = []string{}
	}
	setup := map[string]any{"setup": map[string]any{"model": model, "generationConfig": map[string]any{"responseModalities": []string{"TEXT"}}, "inputAudioTranscription": map[string]any{"languageCodes": []string{}, "customVocabulary": phrases, "mode": "VERBATIM"}}}
	data, _ := json.Marshal(setup)
	err = c.Write(ctx, websocket.MessageText, data)
	if err == nil {
		var b []byte
		_, b, err = c.Read(ctx)
		if err == nil {
			var ev serverEvent
			err = json.Unmarshal(b, &ev)
			if err == nil && ev.SetupComplete != nil && ev.Error == nil {
				return c, nil
			}
		}
	}
	_ = c.CloseNow()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("Ожидание готовности Gemini: %w", ctx.Err())
	}
	return nil, errors.New("Gemini не подтвердил запуск Transcribe Live: проверьте ключ, доступ к модели и квоту")
}

var _ stt.Provider = (*Provider)(nil)
