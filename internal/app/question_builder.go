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
	builders     map[string]*questionbuilder.Builder
	timer        *time.Timer
	cancel       context.CancelFunc
	running      bool
	runChatID    string
	lastStart    time.Time
	models       []llm.Model
	automatic    bool
	autoQueued   bool
	autoTurnID   string
	autoAnswerID string
	// Hold the accepted version while an interrupted generation and its save drain.
	autoRefinement *QuestionCommand
	// prepCancel stops the background analysis-session preparation for prepConfig;
	// prepGen tells a finishing worker whether it is still the current one.
	prepCancel context.CancelFunc
	prepConfig chat.Config
	prepGen    uint64
}

func (q *questionState) init() {
	q.builders = map[string]*questionbuilder.Builder{}
	q.models = []llm.Model{}
}

type QuestionView struct {
	questionbuilder.View
	Automatic bool        `json:"automatic"`
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
	automatic := c.question.automatic
	if a := c.autoAnswers; a != nil {
		a.mu.Lock()
		automatic = a.question.automatic
		a.mu.Unlock()
	}
	return QuestionView{View: c.questionLocked().View(), Automatic: automatic, Available: available, Model: c.settings.Audio.BuilderModel, Reasoning: c.settings.Audio.BuilderReasoning, Models: slices.Clone(c.question.models)}
}
func (c *Controller) pauseQuestionLocked() {
	c.stopAutomaticQuestionLocked()
	if c.question.timer != nil {
		c.question.timer.Stop()
		c.question.timer = nil
	}
	if c.question.cancel != nil {
		c.question.cancel()
	}
	if b := c.question.builders[c.active.ID]; b != nil {
		b.Enable(false)
		if c.autoSession {
			b.Skip()
		}
	}
	c.prepareQuestionLocked()
}

// prepareQuestionLocked keeps one empty analysis session ready while the active chat's builder is
// enabled, so an analysis does not wait for thread/start. No I/O under c.mu: the worker blocks in the
// provider until an analysis claims the session or it is cancelled (disable, model change, Close).
func (c *Controller) prepareQuestionLocked() {
	preparer, ok := c.deps.Provider.(llm.QuestionPreparer)
	cfg := chat.Config{Model: c.settings.Audio.BuilderModel, Reasoning: c.settings.Audio.BuilderReasoning}
	b := c.question.builders[c.active.ID]
	want := ok && !c.closed && cfg.Model != "" && b != nil && b.View().Enabled
	if c.question.prepCancel != nil && (!want || c.question.prepConfig != cfg) {
		c.question.prepCancel()
		c.question.prepCancel = nil
	}
	// A running analysis owns the session; the next one is prepared after it finishes.
	if !want || c.question.prepCancel != nil || c.question.running {
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.question.prepGen++
	gen := c.question.prepGen
	c.question.prepCancel, c.question.prepConfig = cancel, cfg
	c.workers.Add(1)
	go func() {
		defer c.workers.Done()
		defer cancel()
		err := preparer.PrepareQuestionAnalysis(ctx, cfg)
		if err != nil && ctx.Err() == nil {
			c.appendLog(fmt.Sprintf("Подготовка сборщика вопроса: %v\n", err))
		}
		c.mu.Lock()
		if c.question.prepGen == gen {
			c.question.prepCancel = nil
		}
		c.mu.Unlock()
	}()
}
func (c *Controller) scheduleQuestionLocked() {
	c.prepareQuestionLocked()
	if c.closed || c.question.running || c.question.autoRefinement != nil {
		return
	}
	v := c.questionLocked().View()
	if !v.Enabled || v.Manual || v.Editing || v.Error != "" {
		return
	}
	delay := 300 * time.Millisecond
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
	if c.closed {
		c.mu.Unlock()
		return
	}
	defer func() {
		c.mu.Unlock()
		if a := c.autoAnswers; a != nil && s.Track == "system" {
			a.observeAutoQuestion(s)
		}
	}()
	b := c.question.builders[chatID]
	if b == nil {
		return
	}
	b.Observe(s)
	if v := b.View(); v.Error != "" {
		if c.question.cancel != nil && chatID == c.question.runChatID {
			c.question.cancel()
		}
		if chatID == c.active.ID && !v.Enabled {
			c.stopAutomaticQuestionLocked()
		}
	}
	if chatID == c.active.ID && s.Final && s.Track == "system" {
		c.scheduleQuestionLocked()
		c.scheduleAutomaticQuestionLocked()
	}
}

func (c *Controller) observeAutoQuestion(s questionbuilder.Segment) {
	at, _ := time.Parse(time.RFC3339Nano, s.At)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || !c.question.automatic || at.Before(c.autoSince) {
		return
	}
	b := c.questionLocked()
	substantive := s.Final && !questionbuilder.IsFiller(s.Text)
	if s.Final && !substantive {
		// Let a final clear its partial without turning noise into new evidence.
		s.Text = ""
	}
	b.Observe(s)
	v := b.View()
	if !v.Enabled {
		if c.question.cancel != nil {
			c.question.cancel()
		}
		c.stopAutomaticQuestionLocked()
		return
	}
	if substantive && v.Error != "" {
		b.Retry()
	}
	if s.Final {
		c.scheduleQuestionLocked()
		c.scheduleAutomaticQuestionLocked()
	}
}
func (c *Controller) analyzeQuestion() {
	c.mu.Lock()
	c.question.timer = nil
	if c.closed || c.question.running || c.question.autoRefinement != nil {
		c.mu.Unlock()
		return
	}
	analyzer, ok := c.deps.Provider.(llm.QuestionAnalyzer)
	if !ok {
		c.mu.Unlock()
		return
	}
	b := c.questionLocked()
	now := time.Now()
	in, ok := b.Begin(now)
	if !ok && c.question.automatic {
		in, ok = b.BeginDraftCheck(now)
	}
	if !ok {
		c.mu.Unlock()
		return
	}
	in.Automatic = c.question.automatic
	if c.autoSession && in.Automatic {
		in.Recent, in.RecentAnswers = c.autoAnalysisContextLocked()
	}
	cfg := chat.Config{Model: c.settings.Audio.BuilderModel, Reasoning: c.settings.Audio.BuilderReasoning}
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
	// This analysis claims the prepared session (or releases a stale one); it is detached
	// from the controller and cancelled after the analysis, then the next one is prepared.
	claimed := c.question.prepCancel
	c.question.prepCancel = nil
	c.question.prepGen++
	c.workers.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.workers.Done()
		defer cancel()
		started := time.Now()
		result, err := analyzer.AnalyzeQuestion(ctx, in, cfg, func(e llm.Event) {
			if e.Kind == llm.EventProgress {
				c.appendLog(e.Text)
			}
		})
		attrs := []any{"chat", in.ChatID, "model", cfg.Model, "reasoning", cfg.Reasoning, "segments", len(in.Segments), "draft_check", in.DraftCheck, "duration", time.Since(started).Round(time.Millisecond).String()}
		c.mu.Lock()
		defer c.mu.Unlock()
		applyErr := b.Apply(in, result, err)
		if err != nil || applyErr != nil {
			c.logger.Warn("question analysis failed", append(attrs, "error", err, "apply_error", applyErr)...)
			if in.Automatic && c.question.automatic && c.active.ID == in.ChatID {
				b.RetryIfNewSystemSpeech(in.End)
			}
		} else {
			c.logger.Info("question analysis finished", append(attrs, "has_question", result.HasQuestion, "ready", result.Ready, "follow_up", result.FollowUp, "sources", len(result.SourceIDs), c.content("question", result.Text))...)
		}
		if claimed != nil {
			claimed()
		}
		c.question.running = false
		c.question.cancel = nil
		if !c.closed {
			c.scheduleQuestionLocked()
			c.scheduleAutomaticQuestionLocked()
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
	if !c.autoSession && (cmd.Action == "auto-on" || cmd.Action == "auto-off") {
		c.mu.Lock()
		match := c.active.ID == cmd.ChatID && !c.closed
		c.mu.Unlock()
		if !match {
			return ErrStale
		}
		view := c.AutoAnswersSnapshot()
		if cmd.Action == "auto-off" {
			return c.AutoAnswersCommand("pauseAnswers", view.SessionID, "", "")
		}
		return c.AutoAnswersCommand("startAnswers", view.SessionID, "", "")
	}
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
	if cmd.Action == "enable" || cmd.Action == "auto-on" {
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
	if cmd.Action != "auto-on" && cmd.Action != "auto-off" && (cmd.ID != v.ID || cmd.Revision != v.Revision) {
		return ErrStale
	}
	switch cmd.Action {
	case "enable", "auto-on":
		if _, ok := c.deps.Provider.(llm.QuestionAnalyzer); !ok {
			return errors.New("Подключение не поддерживает сборку вопросов")
		}
		if c.settings.Audio.BuilderModel == "" {
			return errors.New("Сначала выберите модель сборщика")
		}
		if !v.Enabled {
			b.Exclude(excluded)
		}
		b.Enable(true)
		if cmd.Action == "auto-on" {
			c.question.automatic = true
		}
	case "auto-off":
		c.stopAutomaticQuestionLocked()
	case "disable":
		c.pauseQuestionLocked()
	case "edit":
		if err := b.Edit(cmd.Text); err != nil {
			return err
		}
		c.question.autoRefinement = nil
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
		c.question.autoRefinement = nil
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
		c.question.autoRefinement = nil
		if c.question.cancel != nil {
			c.question.cancel()
		}
	default:
		return errors.New("Неизвестная команда сборщика")
	}
	c.scheduleQuestionLocked()
	c.scheduleAutomaticQuestionLocked()
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
	if err == nil && c.autoAnswers != nil {
		c.autoAnswers.pauseAutoAnswers("")
	}
	return err
}
