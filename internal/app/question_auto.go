package app

import (
	"errors"
	"slices"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/questionbuilder"
)

// A ready refinement waits for the answer scope's slot. It can interrupt only
// a running automatic answer to the same card.
func (c *Controller) scheduleAutomaticQuestionLocked() {
	if c.closed || !c.question.automatic {
		return
	}
	v, ok := c.automaticQuestionLocked()
	if !ok {
		return
	}
	cmd := QuestionCommand{ChatID: v.ChatID, ID: v.ID, Version: v.Version}
	if c.op.Phase != phaseIdle && c.question.autoRefinement == nil {
		if index := c.automaticReplacementIndex(c.active, v.Card); index >= 0 {
			if _, err := revisedQuestion(autoRevisionBase(c.active, c.active.Messages[index]), v.Card); err == nil {
				c.question.autoRefinement = &cmd
			}
		}
	}
	if c.op.Phase == phaseRequesting && c.op.automaticQuestion && c.op.cancel != nil && !c.op.superseded {
		if c.question.autoRefinement != nil && c.op.messageID == v.PreviousQuestionID {
			c.op.superseded = true
			for key := range c.streams {
				if key.opID == c.op.ID {
					delete(c.streams, key)
				}
			}
			c.scheduleStreamLocked()
			if c.clearCheckpointsLocked(c.op.ID) {
				c.streamPersist.mark()
			}
			c.op.cancel()
		}
	}
	if c.question.autoQueued || c.availableLocked() != nil {
		return
	}
	c.question.autoQueued = true
	c.workers.Add(1)
	go func() {
		defer c.workers.Done()
		_, err := c.submit(SubmitInput{ChatID: cmd.ChatID, RequestID: chat.ID()}, submitOrigin{kind: originQuestion, question: &cmd, automatic: true})
		c.mu.Lock()
		c.question.autoQueued = false
		if err != nil && !errors.Is(err, ErrStale) && !errors.Is(err, ErrBusy) && !errors.Is(err, ErrSavePending) && !c.closed && c.active.ID == cmd.ChatID && c.question.automatic {
			c.stopAutomaticQuestionLocked()
			c.err = err.Error()
			c.changedLocked()
			c.mu.Unlock()
			c.publish()
			return
		}
		c.scheduleAutomaticQuestionLocked()
		c.mu.Unlock()
	}()
}

func (c *Controller) automaticQuestionLocked() (questionbuilder.View, bool) {
	v := c.questionLocked().View()
	if !c.question.automatic || !v.Enabled || v.Manual || v.Editing || v.Origin != questionbuilder.OriginAutomatic || v.Error != "" {
		return v, false
	}
	if pinned := c.question.autoRefinement; pinned != nil {
		return v, pinned.ChatID == v.ChatID && pinned.ID == v.ID && pinned.Version == v.Version
	}
	return v, v.CanSend
}

func (c *Controller) automaticReplacementIndex(doc chat.Chat, card questionbuilder.Card) int {
	if !card.Refinement || card.PreviousQuestionID == "" {
		return -1
	}
	if c.autoSession {
		return slices.IndexFunc(doc.Messages, func(m chat.Message) bool {
			return m.Role == chat.RoleUser && m.ID == card.PreviousQuestionID && m.Question != nil &&
				(m.RequestConfig == nil || !m.RequestConfig.ParallelAnswers)
		})
	}
	if card.PreviousQuestionID != c.question.autoTurnID {
		return -1
	}
	index := len(doc.Messages) - 1
	if index >= 0 && doc.Messages[index].Role == chat.RoleAssistant {
		index--
	}
	if index >= 0 && doc.Messages[index].ID == card.PreviousQuestionID && doc.Messages[index].Question != nil {
		return index
	}
	return -1
}

func revisedQuestion(previous, update questionbuilder.Card) (questionbuilder.Card, error) {
	sources := slices.Clone(previous.Sources)
	for _, source := range update.Sources {
		if !slices.ContainsFunc(sources, func(s questionbuilder.Segment) bool { return s.ID == source.ID }) {
			sources = append(sources, source)
		}
	}
	update.Sources = sources
	update.PreviousQuestionID = previous.PreviousQuestionID
	return update, questionbuilder.ValidateSources(sources)
}

func (c *Controller) stopAutomaticQuestionLocked() {
	c.question.automatic = false
	c.question.autoTurnID = ""
	c.question.autoAnswerID = ""
	c.question.autoRefinement = nil
}

func (c *Controller) stopAutomaticQuestion() {
	if a := c.autoAnswers; a != nil {
		_ = c.pauseAutomaticAnswers("", "")
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopAutomaticQuestionLocked()
	c.scheduleQuestionLocked()
}

func (c *Controller) automaticAudioLost(reason string) {
	if c.autoAnswers == nil {
		c.stopAutomaticQuestion()
		return
	}
	_ = c.pauseAutomaticAnswers("", reason)
}
