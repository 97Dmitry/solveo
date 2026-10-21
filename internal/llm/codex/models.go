package codex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/llm/codex/appserver"
)

// Models reads the catalog through Solveo's own connection and account. It
// never starts a thread or a model turn. Codex may serve its cached/bundled
// catalog when the upstream list is unavailable.
func (c *Client) Models(parent context.Context) ([]llm.Model, error) {
	// A cold start includes launching the process and Codex's own catalog refresh timeout.
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	conn, err := c.app.connection(ctx)
	if err != nil {
		return nil, fmt.Errorf("подключение Codex недоступно: %w", err)
	}
	rpc := func(method string, params, out any) error {
		err := conn.Call(ctx, method, params, out)
		if rpcErr, ok := errors.AsType[*appserver.RPCError](err); ok {
			// Do not expose raw server messages, which may contain account details.
			return fmt.Errorf("запрос каталога Codex %s завершился ошибкой (код %d)", method, rpcErr.Code)
		}
		return err
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
