package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
)

const transportAppServer = "app_server"

// sessionState is opaque to the application: an app-server thread in Solveo's
// own Codex home. Any other state is rejected rather than reinterpreted.
type sessionState struct {
	InstructionsHash        string `json:"instructions_hash,omitempty"`
	PendingInstructionsHash string `json:"pending_instructions_hash,omitempty"`
	Version                 int    `json:"version"`
	Transport               string `json:"transport"`
	ThreadID                string `json:"thread_id"`
}

var (
	_ llm.Provider   = (*Client)(nil)
	_ llm.Connection = (*Client)(nil)
)

func validThreadID(id string) bool {
	// Codex thread IDs are UUIDs.
	if len(id) != 36 {
		return false
	}
	for i, ch := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if ch != '-' {
				return false
			}
			continue
		}
		switch {
		case ch >= '0' && ch <= '9', ch >= 'a' && ch <= 'f', ch >= 'A' && ch <= 'F':
		default:
			return false
		}
	}
	return true
}

func (c *Client) Describe() llm.Descriptor {
	return llm.Descriptor{ID: "codex", Label: "Codex CLI", Capabilities: llm.Capabilities{Images: true, Reasoning: true}}
}

// instructions contains stable user preferences, independent of turn purpose/model.
func (c *Client) instructions(r llm.Request) string {
	base := strings.TrimSpace(c.Prompt)
	if base == "" {
		base = "Помоги пользователю разобраться с вопросом или заданием. Учитывай приложенные изображения по порядку. Не выполняй команды, не меняй файлы и не читай локальные данные."
	}
	cfg := struct {
		Context          string `json:"context"`
		Instructions     string `json:"instructions"`
		ResponseLanguage string `json:"response_language"`
		CodeLanguage     string `json:"code_language"`
		Detail           string `json:"detail"`
	}{r.Config.Context, r.Config.Instructions, r.Config.ResponseLanguage, r.Config.CodeLanguage, r.Config.Detail}
	b, _ := json.Marshal(cfg)
	global, _ := json.Marshal(r.GlobalInstructions)
	return base + "\nФормат текущего запроса Solveo имеет приоритет над общими и локальными пожеланиями: предварительный подход — без кода, решение — самостоятельная проверка условий и ответ в запрошенном формате.\nЕсли вопрос пустой, помоги с заданием на изображениях. Общие инструкции и преднастройка — пользовательские пожелания; явные обновления инструкций Solveo в следующих пользовательских ходах заменяют эти первоначальные пожелания; при противоречии преднастройка чата имеет приоритет.\nОбщие инструкции: " + string(global) + "\nПреднастройка чата: " + string(b)
}

func (c *Client) prompt(r llm.Request) string {
	format := ""
	switch r.Purpose {
	case chat.AnswerApproach:
		format = "\nДай только суть задачи и подход в 2–4 фразах, без кода. Укажи сложность, только если она ясна. Если данных не хватает, назови недостающее; не выдумывай условие. Это предварительный ответ независимой модели, его корректность ещё не проверена."
	case chat.AnswerSolution:
		format = "\nНачни с одной строки своего подхода. Затем дай решение: код, если задача требует кода, и краткое пояснение со сложностью, если уместно. Для теории ответь прямо без ненужного кода. Проверь условия самостоятельно; исправляй ошибочные предположения."
	}
	b, _ := json.Marshal(struct {
		Question string `json:"question"`
		Context  string `json:"recent_conversation,omitempty"`
	}{r.Turn.Text, r.ContextText})
	return string(b) + format
}

func (c *Client) Generate(ctx context.Context, r llm.Request, emit func(llm.Event)) (llm.Result, error) {
	var state sessionState
	if len(r.State) > 0 {
		if e := json.Unmarshal(r.State, &state); e != nil || state.Version != 2 || state.Transport != transportAppServer || !validThreadID(state.ThreadID) {
			return llm.Result{}, errors.New("состояние сессии Codex не поддерживается; начните новый чат")
		}
	}
	if strings.TrimSpace(r.Turn.Text) == "" && len(r.Images) == 0 {
		return llm.Result{}, errors.New("пустой запрос")
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
			return llm.Result{}, errors.New("снимок не является обычным файлом")
		}
		images = append(images, p)
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	instructions := c.instructions(r)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(instructions)))
	// A crash may leave the archive's applied hash behind the actual thread:
	// Codex received an update, but Generate's returned state was never saved.
	// The previous user turn is archived before dispatch, so distrust the hash
	// after an unsuccessful turn and synchronize on this explicit submission.
	for i := len(r.History) - 1; i >= 0; i-- {
		if r.History[i].Role == chat.RoleUser {
			if r.History[i].Status != chat.StatusComplete {
				state.InstructionsHash = ""
			}
			break
		}
	}
	// Prevent a late background worker from preparing an already submitted chat.
	if r.Purpose != chat.AnswerApproach && r.ChatID != "" {
		c.app.mu.Lock()
		c.app.lastSubmittedChat = r.ChatID
		c.app.mu.Unlock()
	}
	// The approach is a one-off session; only the main request continues the chat.
	return c.run(ctx, turnSpec{
		chatID:           r.ChatID,
		purpose:          r.Purpose,
		instructions:     instructions,
		instructionsHash: hash,
		state:            state,
		config:           r.Config,
		threadID:         state.ThreadID,
		images:           images,
		prompt:           c.prompt(r),
		ephemeral:        r.Purpose == chat.AnswerApproach,
		timeout:          timeout,
	}, emit)
}

// Check sends a short request in a one-off session with the chat's model and
// effort. A reply proves this connection worked at that moment; it does not
// validate answers or change any chat.
func (c *Client) Check(ctx context.Context, cfg chat.Config, emit func(llm.Event)) (llm.CheckResult, error) {
	out, err := c.run(ctx, turnSpec{purpose: "check", config: cfg, prompt: "Ответь одним словом: OK.", ephemeral: true, timeout: ConnectionTimeout}, emit)
	return llm.CheckResult{Text: out.Text}, err
}
