package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/97Dmitry/solveo/internal/llm"
)

// Models uses the authenticated CLI's catalog, never starts a thread or a model turn.
// App-server may use its own cached/bundled catalog if upstream is unavailable.
func (c *Client) Models(parent context.Context) ([]llm.Model, error) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	workdir, err := os.MkdirTemp("", "solveo-models-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workdir)
	cmd := exec.CommandContext(ctx, c.binary(), "app-server", "-c", `model_provider="openai"`)
	cmd.Dir = workdir
	configureSubprocess(cmd)
	cmd.WaitDelay = 2 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	defer func() { _ = stdin.Close() }()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	// Do not expose raw stderr, which may contain local/account information.
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("не удалось запустить загрузку моделей Codex: %w", err)
	}
	defer func() { cancel(); _ = stdin.Close(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(io.LimitReader(stdout, 16<<20))
	scanner.Buffer(make([]byte, 4096), 2<<20)
	encoder := json.NewEncoder(stdin)
	nextID := 0
	rpc := func(method string, params interface{}, out interface{}) error {
		nextID++
		if e := encoder.Encode(struct {
			ID     int         `json:"id"`
			Method string      `json:"method"`
			Params interface{} `json:"params"`
		}{nextID, method, params}); e != nil {
			return e
		}
		for scanner.Scan() {
			var envelope struct {
				ID     *int            `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if e := json.Unmarshal(scanner.Bytes(), &envelope); e != nil {
				return errors.New("некорректный ответ каталога моделей Codex")
			}
			if envelope.ID == nil || *envelope.ID != nextID {
				continue
			}
			if envelope.Error != nil {
				return fmt.Errorf("запрос каталога Codex %s завершился ошибкой (код %d)", method, envelope.Error.Code)
			}
			if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
				return errors.New("каталог Codex не вернул результат")
			}
			return json.Unmarshal(envelope.Result, out)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e := scanner.Err(); e != nil {
			return e
		}
		return errors.New("загрузка моделей Codex прервалась до ответа; обновите Codex CLI и проверьте вход")
	}
	var initialized map[string]interface{}
	if err = rpc("initialize", map[string]interface{}{"clientInfo": map[string]string{"name": "solveo", "title": "Solveo", "version": "0.1.0"}}, &initialized); err != nil {
		return nil, err
	}
	if err = encoder.Encode(map[string]interface{}{"method": "initialized", "params": map[string]interface{}{}}); err != nil {
		return nil, err
	}
	models := make([]llm.Model, 0)
	seenModels := map[string]bool{}
	seenCursors := map[string]bool{}
	cursor := ""
	for page := 0; page < 100; page++ {
		var response struct {
			Data []struct {
				ID               string `json:"id"`
				Model            string `json:"model"`
				DisplayName      string `json:"displayName"`
				Description      string `json:"description"`
				Hidden           bool   `json:"hidden"`
				IsDefault        bool   `json:"isDefault"`
				DefaultReasoning string `json:"defaultReasoningEffort"`
				Efforts          []struct {
					ID          string `json:"reasoningEffort"`
					Description string `json:"description"`
				} `json:"supportedReasoningEfforts"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		params := map[string]interface{}{"limit": 100, "includeHidden": false}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if err = rpc("model/list", params, &response); err != nil {
			return nil, err
		}
		if response.Data == nil {
			return nil, errors.New("каталог моделей Codex не вернул данные")
		}
		for _, entry := range response.Data {
			if entry.Hidden {
				continue
			}
			id := strings.TrimSpace(entry.Model)
			if id == "" || len(id) > 256 || strings.ContainsAny(id, "\x00\r\n") || strings.TrimSpace(entry.DisplayName) == "" {
				return nil, errors.New("некорректная модель в каталоге Codex")
			}
			if seenModels[id] {
				continue
			}
			seenModels[id] = true
			m := llm.Model{ID: id, Label: entry.DisplayName, Description: entry.Description, IsDefault: entry.IsDefault, DefaultReasoning: entry.DefaultReasoning, ReasoningEfforts: make([]llm.ReasoningEffort, 0, len(entry.Efforts))}
			seenEfforts := map[string]bool{}
			for _, effort := range entry.Efforts {
				if effort.ID == "" || !llm.ValidReasoning(effort.ID) {
					return nil, errors.New("некорректный уровень рассуждения в каталоге Codex")
				}
				if !seenEfforts[effort.ID] {
					m.ReasoningEfforts = append(m.ReasoningEfforts, llm.ReasoningEffort{ID: effort.ID, Description: effort.Description})
					seenEfforts[effort.ID] = true
				}
			}
			if m.DefaultReasoning != "" && !seenEfforts[m.DefaultReasoning] {
				return nil, errors.New("некорректный уровень рассуждения по умолчанию в каталоге Codex")
			}
			models = append(models, m)
		}
		if response.NextCursor == nil || *response.NextCursor == "" {
			if len(models) == 0 {
				return nil, errors.New("Codex не вернул доступных моделей; проверьте вход")
			}
			return models, nil
		}
		cursor = *response.NextCursor
		if seenCursors[cursor] {
			return nil, errors.New("каталог Codex повторил курсор страницы")
		}
		seenCursors[cursor] = true
	}
	return nil, errors.New("каталог моделей Codex превышает лимит страниц")
}
