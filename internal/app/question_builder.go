package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/questionbuilder"
)

// All question state is protected by c.mu; never acquire audio.mu from here.
type questionState struct {
	builders  map[string]*questionbuilder.Builder
	timer     *time.Timer
	cancel    context.CancelFunc
	running   bool
	runChatID string
	lastStart time.Time
	models    []llm.Model
}

func (q *questionState) init() {
	q.builders = map[string]*questionbuilder.Builder{}
	q.models = []llm.Model{}
}

type QuestionView struct {
	questionbuilder.View
	Available bool        `json:"available"`
	Model     string      `json:"model"`
	Reasoning string      `json:"reasoning"`
	Models    []llm.Model `json:"models"`
}
type QuestionCommand struct {
	Version   uint64 `json:"version"`
	ChatID    string `json:"chat_id"`
	ID        string `json:"id"`
	Revision  uint64 `json:"revision"`
	Action    string `json:"action"`
	Text      string `json:"text"`
	Confirm   bool   `json:"confirm"`
	Model     string `json:"model"`
	Reasoning string `json:"reasoning"`
}

func (c *Controller) questionLocked() *questionbuilder.Builder {
	b := c.question.builders[c.active.ID]
	if b != nil {
		return b
	}
	b = questionbuilder.New(c.active.ID)
	recent := []questionbuilder.Card{}
	for _, m := range c.active.Messages {
		if m.Question != nil {
			card := *m.Question
			card.ID = m.ID
			recent = append(recent, card)
		}
	}
	b.RestoreRecent(recent)
	c.question.builders[c.active.ID] = b
	return b
}
func (c *Controller) QuestionSnapshot() QuestionView {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.questionSnapshotLocked()
}
func (c *Controller) questionSnapshotLocked() QuestionView {
	_, available := c.deps.Provider.(llm.QuestionAnalyzer)
	return QuestionView{View: c.questionLocked().View(), Available: available, Model: c.settings.Audio.BuilderModel, Reasoning: c.settings.Audio.BuilderReasoning, Models: slices.Clone(c.question.models)}
}
func (c *Controller) pauseQuestionLocked() {
	if c.question.timer != nil {
		c.question.timer.Stop()
		c.question.timer = nil
	}
	if c.question.cancel != nil {
		c.question.cancel()
	}
	if b := c.question.builders[c.active.ID]; b != nil {
		b.Enable(false)
	}
}
func (c *Controller) scheduleQuestionLocked() {
	if c.closed || c.question.running {
		return
	}
	v := c.questionLocked().View()
	if !v.Enabled || v.Manual || v.Editing || v.Error != "" {
		return
	}
	delay := 800 * time.Millisecond
	if d := time.Until(c.question.lastStart.Add(2 * time.Second)); d > delay {
		delay = d
	}
	if c.question.timer != nil {
		c.question.timer.Stop()
	}
	c.question.timer = time.AfterFunc(delay, c.analyzeQuestion)
}
func (c *Controller) observeQuestion(chatID string, s questionbuilder.Segment) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	b := c.question.builders[chatID]
	if b == nil {
		return
	}
	b.Observe(s)
	if b.View().Error != "" && c.question.cancel != nil && chatID == c.question.runChatID {
		c.question.cancel()
	}
	if chatID == c.active.ID && s.Final && s.Track == "system" {
		c.scheduleQuestionLocked()
	}
}
func (c *Controller) analyzeQuestion() {
	c.mu.Lock()
	c.question.timer = nil
	if c.closed || c.question.running {
		c.mu.Unlock()
		return
	}
	analyzer, ok := c.deps.Provider.(llm.QuestionAnalyzer)
	if !ok {
		c.mu.Unlock()
		return
	}
	b := c.questionLocked()
	in, ok := b.Begin(time.Now())
	if !ok {
		c.mu.Unlock()
		return
	}
	cfg := chat.Config{Model: c.settings.Audio.BuilderModel, Reasoning: c.settings.Audio.BuilderReasoning}
	// Only interview facts, never answer-format/global instructions or full history.
	contextText := strings.Join([]string{c.active.Config.Position, c.active.Config.Stack, c.active.Config.InterviewFormat, c.active.Config.Context}, "\n")
	runes := []rune(contextText)
	if len(runes) > 4000 {
		runes = runes[:4000]
	}
	in.Context = string(runes)
	ctx, cancel := context.WithTimeout(c.ctx, 15*time.Second)
	c.question.cancel = cancel
	c.question.running = true
	c.question.runChatID = in.ChatID
	c.question.lastStart = time.Now()
	c.workers.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.workers.Done()
		defer cancel()
		result, err := analyzer.AnalyzeQuestion(ctx, in, cfg)
		c.mu.Lock()
		defer c.mu.Unlock()
		_ = b.Apply(in, result, err)
		c.question.running = false
		c.question.cancel = nil
		if !c.closed {
			c.scheduleQuestionLocked()
		}
	}()
}
func (c *Controller) acceptQuestionLocked(cmd QuestionCommand) (questionbuilder.Card, error) {
	v := c.questionLocked().View()
	if cmd.ChatID != c.active.ID || cmd.ID != v.ID || cmd.Version != v.Version {
		return questionbuilder.Card{}, ErrStale
	}
	if !v.CanSend {
		return questionbuilder.Card{}, errors.New("Вопрос ещё уточняется или требует ручного сохранения")
	}
	return v.Card, nil
}
func (c *Controller) SendQuestion(cmd QuestionCommand) (string, error) {
	return c.submit(SubmitInput{ChatID: cmd.ChatID, RequestID: chat.ID()}, submitOrigin{kind: originQuestion, question: &cmd})
}
func (c *Controller) QuestionCommand(cmd QuestionCommand) error {
	if cmd.Action == "send" {
		_, err := c.SendQuestion(cmd)
		return err
	}
	if cmd.Action == "models" {
		models, err := c.ListModels()
		if err != nil {
			return err
		}
		c.mu.Lock()
		c.question.models = models
		c.mu.Unlock()
		return nil
	}
	if cmd.Action == "configure" {
		return c.configureQuestion(cmd)
	}
	var excluded []string
	if cmd.Action == "enable" {
		c.audio.mu.Lock()
		for _, s := range c.audio.segments {
			if !s.Final {
				excluded = append(excluded, s.ID)
			}
		}
		c.audio.mu.Unlock()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || cmd.ChatID != c.active.ID {
		return ErrStale
	}
	b := c.questionLocked()
	v := b.View()
	if cmd.ID != v.ID || cmd.Revision != v.Revision {
		return ErrStale
	}
	switch cmd.Action {
	case "enable":
		if _, ok := c.deps.Provider.(llm.QuestionAnalyzer); !ok {
			return errors.New("Подключение не поддерживает сборку вопросов")
		}
		if c.settings.Audio.BuilderModel == "" {
			return errors.New("Сначала выберите модель сборщика")
		}
		b.Exclude(excluded)
		b.Enable(true)
	case "disable":
		c.pauseQuestionLocked()
	case "edit":
		if err := b.Edit(cmd.Text); err != nil {
			return err
		}
		if c.question.cancel != nil {
			c.question.cancel()
		}
	case "save":
		if !v.Editing {
			return errors.New("Карточка изменена или правка не активна; сохранение отклонено")
		}
		if err := b.Edit(cmd.Text); err != nil {
			return err
		}
		if err := b.SaveEdit(); err != nil {
			return err
		}
	case "confirm":
		if err := b.Confirm(); err != nil {
			return err
		}
	case "rebuild":
		if (v.Manual || v.Editing) && v.Text != "" && !cmd.Confirm {
			return errors.New("Подтвердите замену ручной версии вопроса")
		}
		b.Rebuild()
		if c.question.cancel != nil {
			c.question.cancel()
		}
	case "retry":
		b.Retry()
	case "skip":
		if v.ID == "" && v.Status != "analyzing" && v.Error == "" {
			return errors.New("Нет вопроса для пропуска")
		}
		b.Skip()
		if c.question.cancel != nil {
			c.question.cancel()
		}
	default:
		return errors.New("Неизвестная команда сборщика")
	}
	c.scheduleQuestionLocked()
	return nil
}
func (c *Controller) configureQuestion(cmd QuestionCommand) error {
	if cmd.Model == "" || len(cmd.Model) > 256 || !utf8.ValidString(cmd.Model) {
		return errors.New("Выберите модель сборщика")
	}
	models, err := c.ListModels()
	if err != nil {
		return err
	}
	found := false
	for _, m := range models {
		if m.ID == cmd.Model {
			found = cmd.Reasoning == ""
			for _, e := range m.ReasoningEfforts {
				found = found || e.ID == cmd.Reasoning
			}
		}
	}
	if !found {
		return fmt.Errorf("модель или effort недоступны: %s", cmd.Model)
	}
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	c.mu.Lock()
	if c.closed || cmd.ChatID != c.active.ID {
		c.mu.Unlock()
		return ErrStale
	}
	cfg := c.settings
	c.pauseQuestionLocked()
	c.mu.Unlock()
	cfg.Audio.BuilderModel = cmd.Model
	cfg.Audio.BuilderReasoning = cmd.Reasoning
	_, err = c.saveSettingsLocked(cfg)
	return err
}
