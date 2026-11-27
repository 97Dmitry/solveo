package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/stt"
)

// autoStart belongs to the parent under c.mu. Capture and provider state remain
// under audio.mu; waiting on audio changes never holds either mutex.
type autoStart struct {
	generation uint64
	cancel     context.CancelFunc
	err        string
}

type autoAudioState struct {
	model, busy, err, setupHint string
	available, systemActive     bool
	checkBeforeStart            bool
}

func (c *Controller) autoAudioLocked() autoAudioState {
	a := &c.audio
	s := autoAudioState{model: a.model, busy: a.busyState(), err: a.err}
	if p := c.audioProvider(); p != nil {
		s.available = true
		d := p.Describe()
		if a.model != audioModelReady && d.NeedsKey && !a.keys[d.ID] {
			s.setupHint = "Укажите " + d.KeyLabel + " в настройках аудио."
		}
		if d.Preparable && a.model != audioModelReady {
			s.checkBeforeStart = len(a.checks) == 0
			for _, check := range a.checks {
				if check.State == stt.StateMissing {
					s.setupHint = "Подготовьте распознаватель в настройках аудио."
					break
				}
			}
		}
	}
	for _, source := range a.sources {
		if source.view.Track == "system" {
			s.systemActive = source.view.Active
			if !s.systemActive && source.view.Error != "" && s.err == "" {
				s.err = source.view.Error
			}
		}
	}
	return s
}

func (c *Controller) startAutomaticAnswers(sessionID string) error {
	c.audio.mu.Lock()
	input := c.autoAudioLocked()
	c.audio.mu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.autoAnswers
	if c.closed || a == nil {
		return ErrStale
	}
	if !input.available {
		return errors.New("Распознавание речи недоступно")
	}
	if c.settings.Audio.BuilderModel == "" {
		return errors.New("Выберите модель сборщика в настройках аудио")
	}
	if input.setupHint != "" {
		return errors.New(input.setupHint)
	}
	if _, ok := c.deps.Provider.(llm.QuestionAnalyzer); !ok {
		return errors.New("Подключение не поддерживает сборку вопросов")
	}
	a.mu.Lock()
	match, running := a.active.ID == sessionID && !a.closed, a.question.automatic
	available := a.availableLocked()
	a.mu.Unlock()
	if !match {
		return ErrStale
	}
	if c.autoStart.cancel != nil || (running && input.systemActive) {
		return nil
	}
	if available != nil {
		return available
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.autoStart.generation++
	generation := c.autoStart.generation
	c.autoStart.cancel, c.autoStart.err = cancel, ""
	c.logger.Info("automatic answers starting", "session", sessionID)
	c.workers.Add(1)
	go func() {
		defer c.workers.Done()
		defer cancel()
		err := c.ensureAutoAudio(ctx)
		if err == nil {
			c.audio.mu.Lock()
			if input := c.autoAudioLocked(); !input.systemActive {
				err = errors.New("Системный звук недоступен")
				if input.err != "" {
					err = errors.New(input.err)
				}
			}
			c.audio.mu.Unlock()
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.autoStart.generation != generation {
			return
		}
		c.autoStart.cancel = nil
		if c.closed {
			return
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			cfg := c.settings.Audio
			preset := chat.Chat{Config: c.active.Config, GlobalInstructions: c.active.GlobalInstructions}
			err = c.resumeAutoAnswers(sessionID, cfg.BuilderModel, cfg.BuilderReasoning, preset)
		}
		if err != nil {
			c.autoStart.err = uiText(err.Error())
			c.logger.Warn("automatic answers startup failed", "session", sessionID, "error", err)
		} else {
			c.logger.Info("automatic answers listening", "session", sessionID)
		}
	}()
	return nil
}

func (c *Controller) ensureAutoAudio(ctx context.Context) error {
	modelRequested, checkRequested := false, false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.audio.mu.Lock()
		input := c.autoAudioLocked()
		changed := c.audio.changeLocked()
		c.audio.mu.Unlock()
		if input.setupHint != "" {
			return errors.New(input.setupHint)
		}
		if input.busy != "" {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changed:
			}
			continue
		}
		if input.model == audioModelReady {
			if err := c.startListening(ctx, true); err != nil {
				return fmt.Errorf("Не удалось открыть звук собеседника: %w", err)
			}
			return nil
		}
		if input.checkBeforeStart && !checkRequested {
			if err := c.RefreshAudio(); err != nil {
				return err
			}
			checkRequested = true
			continue
		}
		if modelRequested {
			if input.err != "" {
				return errors.New(input.err)
			}
			return errors.New("Распознаватель не подключился")
		}
		if err := c.StartAudioModel(); err != nil {
			return err
		}
		modelRequested = true
	}
}

func (c *Controller) pauseAutomaticAnswers(sessionID, cause string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.autoAnswers
	if a == nil {
		return ErrStale
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if sessionID != "" && sessionID != a.active.ID {
		return ErrStale
	}
	wasActive := a.question.automatic || c.autoStart.cancel != nil
	if c.autoStart.cancel != nil {
		c.autoStart.cancel()
		c.autoStart.cancel = nil
		c.autoStart.generation++
	}
	if cause == "" || wasActive {
		c.autoStart.err = uiText(cause)
	}
	a.pauseQuestionLocked()
	if wasActive {
		c.logger.Info("automatic answers paused", "session", a.active.ID, "source_error", c.autoStart.err)
	}
	return nil
}

type autoStatus struct {
	input     autoAudioState
	starting  bool
	err, hint string
}

func (c *Controller) autoStatus() autoStatus {
	c.audio.mu.Lock()
	input := c.autoAudioLocked()
	c.audio.mu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	s := autoStatus{input: input, starting: c.autoStart.cancel != nil, err: c.autoStart.err, hint: input.setupHint}
	if c.settings.Audio.BuilderModel == "" {
		s.hint = "Выберите модель сборщика в настройках аудио."
	}
	return s
}

func (s autoStatus) state(available, running, answering, analyzing, draft bool, err string) string {
	switch {
	case !available || !s.input.available:
		return "unavailable"
	case s.starting && s.input.model != audioModelReady:
		return "starting_model"
	case s.starting:
		return "starting_capture"
	case s.hint != "":
		return "setup"
	case s.err != "" || err != "":
		return "error"
	case !running || !s.input.systemActive:
		return "paused"
	case answering:
		return "answering"
	case analyzing:
		return "analyzing"
	case draft:
		return "awaiting_continuation"
	default:
		return "listening"
	}
}
