package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/questionbuilder"
	"github.com/97Dmitry/solveo/internal/workstate"
)

// startAutoRevisionLocked takes ownership of c.mu. Only the staged attempt
// changes before generation; the question and its last good answer stay intact.
func (c *Controller) startAutoRevisionLocked(doc chat.Chat, index int, card questionbuilder.Card, automatic bool) string {
	m := doc.Messages[index]
	revision := chat.AnswerRevision{
		Question: card,
		Attempt:  chat.Attempt{ID: chat.ID(), Status: chat.StatusRequesting, ConfigRevision: m.ConfigRevision},
	}
	if doc.AnswerRevisions == nil {
		doc.AnswerRevisions = map[string]chat.AnswerRevision{}
	}
	doc.AnswerRevisions[m.ID] = revision
	opID := chat.ID()
	ctx, cancel := context.WithCancel(c.ctx)
	c.op = operation{
		Operation:         Operation{ID: opID, Kind: kindSubmit, Phase: phaseSaving, Message: "Сохраняю уточнение"},
		chatID:            doc.ID,
		messageID:         m.ID,
		revising:          true,
		automaticQuestion: automatic,
		cancel:            cancel,
		started:           time.Now(),
	}
	if automatic {
		c.question.autoRefinement = nil
		c.questionLocked().Sent(m.ID)
		c.scheduleQuestionLocked()
	}
	if c.dropRecoveredLocked(func(b workstate.Block) bool { return b.MessageID == m.ID }) {
		c.streamPersist.mark()
	}
	c.active = chat.Clone(doc)
	c.err = ""
	c.changedLocked()
	c.workers.Add(1)
	c.mu.Unlock()
	c.publish()
	go c.runAutoRevision(ctx, opID, doc, index)
	return opID
}

func (c *Controller) runAutoRevision(ctx context.Context, opID string, doc chat.Chat, index int) {
	defer c.workers.Done()
	original := doc.Messages[index]
	revision := doc.AnswerRevisions[original.ID]
	saved, err := c.deps.Store.Save(history.SaveInput{Chat: doc})
	if err != nil {
		revision.Attempt.Status = chat.StatusFailed
		revision.Attempt.Error = "Уточнение не отправлено: " + err.Error()
		doc.AnswerRevisions[original.ID] = revision
		c.setPending(opID, pendingSave{doc: doc}, err)
		return
	}
	doc = saved
	turn := original
	turn.Text, turn.Question, turn.Status = revision.Question.Text, &revision.Question, chat.StatusRequesting
	turn.Attempts = append(slices.Clone(turn.Attempts), revision.Attempt)
	config := doc.Config
	if original.RequestConfig != nil {
		config = *original.RequestConfig
	}
	turn.RequestConfig = &config
	next := index + 1
	ref := &llm.AnswerContext{MessageID: original.ID, Question: original.Text}
	if next < len(doc.Messages) && doc.Messages[next].Role == chat.RoleAssistant {
		ref.Answer = truncateRunes(doc.Messages[next].Text, 8000)
		next++
	}
	prior := append(slices.Clone(doc.Messages[:index]), doc.Messages[next:]...)
	c.mu.Lock()
	c.active = chat.Clone(doc)
	c.rememberLocked(doc)
	c.op.Phase = phaseRequesting
	c.op.Message = "Уточняю ответ"
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	result, err := c.deps.Provider.Generate(ctx, llm.Request{
		ChatID: doc.ID, GlobalInstructions: doc.GlobalInstructions,
		History: prior, Revision: ref, Turn: turn, Config: config, ConfigRevision: turn.ConfigRevision,
		AutoAnswers: committedAutoAnswers(doc, original.ID), State: doc.ProviderState, Images: []llm.Image{},
	}, c.streamSink(opID, doc.ID, turn.ID, revision.Attempt.ID, blockMessage))
	c.closeStream(opID, revision.Attempt.ID, blockMessage)
	if len(result.State) > 0 {
		doc.ProviderState = result.State
	}
	c.mu.Lock()
	if c.op.superseded && err == nil {
		err = context.Canceled
	}
	if err == nil && strings.TrimSpace(result.Text) == "" {
		err = errors.New("Codex не вернул уточнённый ответ")
	}
	if err == nil {
		commitAutoRevision(&doc, index, next, turn, result.Text)
	} else {
		revision.Attempt.Status = outcomeStatus(err)
		revision.Attempt.Error = err.Error()
		revision.Superseded = c.op.supersededCancellation(revision.Attempt.Status)
		doc.AnswerRevisions[original.ID] = revision
	}
	c.op.Phase = phaseSaving
	c.op.Message = "Сохраняю уточнённый ответ"
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	p := pendingSave{doc: doc}
	saved, saveErr := c.timedSave(history.SaveInput{Chat: doc})
	if saveErr != nil {
		c.setPending(opID, p, saveErr)
		return
	}
	c.finishSave(opID, saved, p)
}

// The provider thread can contain an unsuccessful or discarded revision. Supply
// recent committed pairs so the next answer can distinguish the visible history.
func committedAutoAnswers(doc chat.Chat, excludeID string) []llm.AnswerContext {
	const limit = 6
	var answers []llm.AnswerContext
	for i := len(doc.Messages) - 2; i >= 0 && len(answers) < limit; i-- {
		m, answer := doc.Messages[i], doc.Messages[i+1]
		if m.Role != chat.RoleUser || m.ID == excludeID || m.Status != chat.StatusComplete || answer.Role != chat.RoleAssistant {
			continue
		}
		answers = append(answers, llm.AnswerContext{
			MessageID: m.ID, Question: truncateRunes(m.Text, 640), Answer: truncateRunes(answer.Text, 1000),
		})
	}
	slices.Reverse(answers)
	return answers
}

func autoRevisionBase(doc chat.Chat, message chat.Message) questionbuilder.Card {
	if staged, ok := doc.AnswerRevisions[message.ID]; ok && (staged.Attempt.Status == chat.StatusRequesting || staged.Superseded) {
		return staged.Question
	}
	return *message.Question
}

func commitAutoRevision(doc *chat.Chat, index, next int, turn chat.Message, text string) {
	turn.Status = chat.StatusComplete
	turn.Attempts[len(turn.Attempts)-1].Status = chat.StatusComplete
	doc.Messages[index] = turn
	if next > index+1 {
		doc.Messages[index+1].Text = text
	} else {
		answer := chat.Message{
			ID: chat.ID(), Role: chat.RoleAssistant, Text: text, Status: chat.StatusComplete,
			ConfigRevision: turn.ConfigRevision, AttachmentIDs: []string{}, Attempts: []chat.Attempt{},
		}
		doc.Messages = slices.Insert(doc.Messages, next, answer)
	}
	delete(doc.AnswerRevisions, turn.ID)
}

func (c *Controller) retryAutoRevision(sessionID, cardID, revisionID string) (bool, error) {
	c.mu.Lock()
	revision, ok := c.active.AnswerRevisions[cardID]
	if !ok {
		c.mu.Unlock()
		return false, nil
	}
	if c.active.ID != sessionID || revision.Attempt.ID != revisionID {
		c.mu.Unlock()
		return true, ErrStale
	}
	if err := c.availableLocked(); err != nil {
		c.mu.Unlock()
		return true, err
	}
	index := slices.IndexFunc(c.active.Messages, func(m chat.Message) bool { return m.ID == cardID })
	if index < 0 {
		c.mu.Unlock()
		return true, ErrStale
	}
	c.startAutoRevisionLocked(chat.Clone(c.active), index, revision.Question, false)
	return true, nil
}
