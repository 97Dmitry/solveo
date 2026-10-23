package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
)

// approachContext is deliberately independent of unchecked earlier approaches.
// Current image-only follow-ups also receive the latest prior question's images.
func approachContext(messages []chat.Message) string {
	const limit = 16000
	var lines []string
	remaining := limit
	for i := len(messages) - 1; i >= 0 && remaining > 0; i-- {
		m := messages[i]
		text := m.Text
		for _, a := range m.Answers {
			if a.Kind == chat.AnswerSolution && a.Status == chat.StatusComplete {
				text += "\nРешение: " + a.Text
			}
		}
		if strings.TrimSpace(text) == "" && len(m.AttachmentIDs) == 0 {
			continue
		}
		if len(m.AttachmentIDs) > 0 {
			text += "\n[У вопроса были изображения; они могут отсутствовать в этом контексте.]"
		}
		text = m.Role + ": " + text
		runes := []rune(text)
		if len(runes) > remaining {
			text = "[Ранний контекст сокращён.]\n" + string(runes[len(runes)-remaining:])
		}
		lines = append(lines, text)
		remaining -= len([]rune(text))
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return strings.Join(lines, "\n\n")
}

// approachHistory ends at the explicitly selected answer, so later tasks do not
// displace its context or images in the independent approach request.
func approachHistory(messages []chat.Message, replyToID string) []chat.Message {
	if replyToID == "" {
		return messages
	}
	for i, m := range messages {
		if m.ID == replyToID {
			return messages[:i+1]
		}
	}
	return nil
}

func (c *Controller) approachImages(doc chat.Chat, current []llm.Image) ([]llm.Image, error) {
	if len(current) > 0 {
		return append([]llm.Image(nil), current...), nil
	}
	last := len(doc.Messages) - 1
	history := approachHistory(doc.Messages[:last], doc.Messages[last].ReplyToID)
	for i := len(history) - 1; i >= 0; i-- {
		m := history[i]
		if m.Role != chat.RoleUser || len(m.AttachmentIDs) == 0 {
			continue
		}
		images := []llm.Image{}
		for _, id := range m.AttachmentIDs {
			for _, a := range doc.Attachments {
				if a.ID == id {
					path, err := c.deps.Store.AttachmentPath(doc.ID, a)
					if err != nil {
						return nil, err
					}
					images = append(images, llm.Image{ID: id, Path: path})
					break
				}
			}
		}
		return images, nil
	}
	return []llm.Image{}, nil
}

type parallelResult struct {
	index   int
	result  llm.Result
	err     error
	elapsed int64
}

func outcomeStatus(err error) string {
	switch {
	case err == nil:
		return chat.StatusComplete
	case errors.Is(err, llm.ErrOutcomeUnknown):
		return chat.StatusUnknown
	case errors.Is(err, context.Canceled):
		return chat.StatusCanceled
	default:
		return chat.StatusFailed
	}
}

// runParallel is the sole owner of doc and archive writes. Both provider workers
// must return before the parent releases the operation or permits RetrySave.
func (c *Controller) runParallel(ctx context.Context, opID string, doc chat.Chat, images []llm.Image, ids []string, draft Draft, clearDraft bool, inputErr error) {
	last := len(doc.Messages) - 1
	turn := doc.Messages[last]
	cfg := *turn.RequestConfig
	attemptID := turn.Attempts[len(turn.Attempts)-1].ID
	fastCtx, cancelFast := context.WithCancel(ctx)
	defer cancelFast()
	results := make(chan parallelResult, 2)
	remaining := 0
	for index, answer := range turn.Answers {
		if answer.Status != chat.StatusRequesting || answer.AttemptID != attemptID {
			continue
		}
		remaining++
		isolated := chat.Clone(doc)
		req := llm.Request{ChatID: isolated.ID, GlobalInstructions: isolated.GlobalInstructions, Purpose: answer.Kind, Turn: isolated.Messages[last], Config: cfg, ConfigRevision: turn.ConfigRevision, History: isolated.Messages[:last], State: isolated.ProviderState, Images: append([]llm.Image(nil), images...)}
		req.Turn.Answers = nil
		req.Turn.RequestConfig = nil
		reqCtx := ctx
		requestErr := inputErr
		if index == 0 {
			reqCtx = fastCtx
			req.State = nil
			contextHistory := approachHistory(req.History, turn.ReplyToID)
			req.ContextText = approachContext(contextHistory)
			req.History = nil
			// The adapter needs the exact target for the quote, not an unbounded
			// conversation duplicated alongside ContextText.
			if turn.ReplyToID != "" && len(contextHistory) > 0 {
				req.History = contextHistory[len(contextHistory)-1:]
			}

			if turn.Question != nil && turn.Question.PreviousQuestionID != "" {
				for _, m := range isolated.Messages[:last] {
					if m.ID == turn.Question.PreviousQuestionID {
						req.History = append(req.History, m)
						break
					}
				}
			}
			req.Config.Model = cfg.FastModel
			req.Config.Reasoning = cfg.FastReasoning
			req.Config.Detail = "brief"
			// A standalone follow-up must see the image that defined the prior task.
			if requestErr == nil {
				req.Images, requestErr = c.approachImages(doc, images)
			}
		}
		sink := c.streamSink(opID, doc.ID, turn.ID, attemptID, answer.Kind)
		go func(index int, reqCtx context.Context, req llm.Request, requestErr error) {
			started := time.Now()
			var result llm.Result
			err := requestErr
			if err == nil {
				result, err = c.deps.Provider.Generate(reqCtx, req, sink)
			}
			results <- parallelResult{index: index, result: result, err: err, elapsed: time.Since(started).Milliseconds()}
		}(index, reqCtx, req, requestErr)
	}
	var saveErr error
	mainComplete := false
	for remaining > 0 {
		out := <-results
		remaining--
		answer := &doc.Messages[last].Answers[out.index]
		c.closeStream(opID, attemptID, answer.Kind)
		answer.ElapsedMS = out.elapsed
		if out.index == 0 && mainComplete {
			// Do not display an obsolete approach arriving after the finished solution.
			answer.Status = chat.StatusCanceled
			answer.Error = "Решение уже готово; предварительный ответ остановлен"
		} else {
			answer.Status = outcomeStatus(out.err)
			if out.err != nil {
				answer.Error = out.err.Error()
			} else {
				answer.Text = out.result.Text
			}
		}
		if out.index == 1 {
			if len(out.result.State) > 0 {
				doc.ProviderState = out.result.State
			}
			doc.Messages[last].Status = answer.Status
			attempt := &doc.Messages[last].Attempts[len(doc.Messages[last].Attempts)-1]
			attempt.Status = answer.Status
			attempt.Error = answer.Error
			mainComplete = out.err == nil
			if mainComplete {
				// A late preliminary approach must not appear after the solution.
				c.closeStream(opID, attemptID, chat.AnswerApproach)
				cancelFast()
			}
		}
		c.mu.Lock()
		if c.op.ID == opID {
			c.active = chat.Clone(doc)
			if out.index == 1 && out.err != nil {
				c.err = out.err.Error()
			}
			c.changedLocked()
		}
		c.mu.Unlock()
		c.publish()
		if remaining > 0 && saveErr == nil {
			var saved chat.Chat
			saved, saveErr = c.deps.Store.Save(history.SaveInput{Chat: doc})
			if saveErr == nil {
				doc = saved
			} else {
				// Cancel both, then collect any accepted main outcome/state before recovery.
				c.mu.Lock()
				if c.op.ID == opID && c.op.cancel != nil {
					c.op.cancel()
				}
				c.mu.Unlock()
			}
		}
	}
	p := pendingSave{doc: doc, draft: draft, clearDraft: clearDraft && mainComplete}
	if mainComplete {
		p.cleanupIDs = ids
	}
	if saveErr != nil {
		c.setPending(opID, p, saveErr)
		return
	}
	c.mu.Lock()
	if c.op.ID == opID {
		c.op.Phase = phaseSaving
		c.op.Message = "Сохраняю результат"
		c.changedLocked()
	}
	c.mu.Unlock()
	c.publish()
	saved, err := c.timedSave(history.SaveInput{Chat: doc})
	if err != nil {
		c.setPending(opID, p, err)
		return
	}
	c.finishSave(opID, saved, p)
}
