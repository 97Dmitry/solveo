package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type sessionState struct {
	Version   int    `json:"version"`
	SessionID string `json:"session_id"`
}

var _ llm.Provider = (*Client)(nil)

func New(binary, prompt string, timeout time.Duration) *Client {
	return &Client{Binary: binary, Prompt: prompt, Timeout: timeout}
}
func (c *Client) Describe() llm.Descriptor {
	return llm.Descriptor{ID: "codex", Label: "Codex CLI", Capabilities: llm.Capabilities{Images: true, Reasoning: true}}
}
func (c *Client) prompt(r llm.Request) string {
	base := strings.TrimSpace(c.Prompt)
	if base == "" {
		base = "Помоги пользователю разобраться с вопросом или заданием. Учитывай приложенные изображения по порядку. Не выполняй команды, не меняй файлы и не читай локальные данные."
	}
	// JSON separates configuration fields and arbitrary user text without shell interpolation.
	b, _ := json.MarshalIndent(struct {
		Revision uint64      `json:"configuration_revision"`
		Config   chat.Config `json:"configuration"`
		Question string      `json:"question"`
	}{r.ConfigRevision, r.Config, r.Turn.Text}, "", "  ")
	return base + "\nНастройка действует для этого и следующих вопросов, пока не будет изменена. Если вопрос пустой, помоги с заданием на изображениях.\n" + string(b)
}
func (c *Client) Generate(ctx context.Context, r llm.Request, emit func(llm.Event)) (llm.Result, error) {
	var state sessionState
	if len(r.State) > 0 {
		if e := json.Unmarshal(r.State, &state); e != nil || state.Version != 1 || !validThreadID(state.SessionID) {
			return llm.Result{}, errors.New("invalid Codex provider state")
		}
	}
	if strings.TrimSpace(r.Turn.Text) == "" && len(r.Images) == 0 {
		return llm.Result{}, errors.New("empty request")
	}
	images := make([]string, 0, len(r.Images))
	for _, img := range r.Images {
		p, e := filepath.Abs(img.Path)
		if e != nil {
			return llm.Result{}, e
		}
		info, e := os.Stat(p)
		if e != nil {
			return llm.Result{}, e
		}
		if !info.Mode().IsRegular() {
			return llm.Result{}, errors.New("image is not a regular file")
		}
		images = append(images, p)
	}
	worker := *c
	worker.Model = r.Config.Model
	worker.Reasoning = r.Config.Reasoning
	worker.Progress = func(s string) {
		if emit != nil {
			emit(llm.Event{Kind: "progress", Text: s})
		}
	}
	text, id, e := worker.runSession(ctx, images, c.prompt(r), state.SessionID)
	result := llm.Result{}
	if id != "" {
		if state.SessionID != "" && state.SessionID != id {
			return result, fmt.Errorf("%w: Codex resumed a different session; answer discarded", llm.ErrOutcomeUnknown)
		}
		result.State, _ = json.Marshal(sessionState{1, id})
	} else if len(r.State) > 0 {
		result.State = append(json.RawMessage(nil), r.State...)
	}
	if e != nil {
		return result, e
	}
	result.Text = text
	return result, nil
}
func (c *Client) Check(ctx context.Context, cfg chat.Config, emit func(llm.Event)) (llm.CheckResult, error) {
	worker := *c
	worker.Model = cfg.Model
	worker.Reasoning = cfg.Reasoning
	worker.Progress = func(s string) {
		if emit != nil {
			emit(llm.Event{Kind: "progress", Text: s})
		}
	}
	text, e := worker.CheckConnection(ctx)
	return llm.CheckResult{Text: text}, e
}
