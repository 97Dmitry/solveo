package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/questionbuilder"
	"github.com/97Dmitry/solveo/internal/workstate"
)

// submitOrigin tells which source a question comes from. Only the composer owns the draft:
// quick capture and the transcript never read, acknowledge or clear it.
type submitOrigin struct {
	kind      int
	question  *QuestionCommand
	captureID string // capture operation that may submit while it still owns the slot
}

const (
	originComposer = iota
	originCapture
	originTranscript
	originQuestion
)

func (c *Controller) Submit(in SubmitInput) (string, error) {
	return c.submit(in, submitOrigin{kind: originComposer})
}
func (c *Controller) submit(in SubmitInput, origin submitOrigin) (string, error) {
	c.mu.Lock()
	if e := c.availableLocked(); e != nil && (origin.kind != originCapture || c.op.ID != origin.captureID || c.op.Phase != phaseCapturing) {
		c.mu.Unlock()
		return "", e
	}
	if in.ChatID != c.active.ID || in.RequestID == "" {
		c.mu.Unlock()
		return "", ErrStale
	}
	if c.requests[in.RequestID] {
		c.mu.Unlock()
		return "", ErrStale
	}
	if c.active.ProviderID != c.deps.Provider.Describe().ID {
		c.mu.Unlock()
		return "", errors.New("подключение LLM недоступно для этого чата")
	}
	if in.IntentID != "" {
		if c.intent == nil || in.IntentID != c.intent.ID || in.ChatID != c.intent.ChatID || c.deps.Now().UnixMilli() >= c.intent.ExpiresAt || in.DraftRevision < c.intent.DraftRevision {
			c.mu.Unlock()
			return "", ErrStale
		}
	}
	if in.RetryMessageID == "" && !chat.ValidReference(c.active.Messages, in.ReplyToID, in.ReplyToKind) {
		c.mu.Unlock()
		return "", errors.New("выбранный ответ недоступен для уточнения")
	}
	var questionCard *questionbuilder.Card
	if origin.kind == originQuestion {
		card, err := c.acceptQuestionLocked(*origin.question)
		if err != nil {
			c.mu.Unlock()
			return "", err
		}
		questionCard = &card
		in.Text = card.Text
	}
	doc := chat.Clone(c.active)
	var turn chat.Message
	var sources []history.Source
	ids := []string{}
	if in.RetryMessageID != "" {
		if len(doc.Messages) == 0 {
			c.mu.Unlock()
			return "", ErrStale
		}
		last := doc.Messages[len(doc.Messages)-1]
		if last.ID != in.RetryMessageID || last.Role != chat.RoleUser || last.Status == chat.StatusComplete {
			c.mu.Unlock()
			return "", ErrStale
		}
		turn = last
		ids = append(ids, last.AttachmentIDs...)
	} else {
		if strings.TrimSpace(in.Text) == "" && len(in.AttachmentIDs) == 0 {
			c.mu.Unlock()
			return "", ErrEmpty
		}
		if len(in.Text) > 1<<20 {
			c.mu.Unlock()
			return "", errors.New("вопрос длиннее 1 МиБ")
		}
		if len(in.AttachmentIDs) > workstate.MaxQueue {
			c.mu.Unlock()
			return "", ErrQueueFull
		}
		wanted := map[string]bool{}
		for _, id := range in.AttachmentIDs {
			if wanted[id] {
				c.mu.Unlock()
				return "", errors.New("снимок указан дважды")
			}
			wanted[id] = true
		}
		for _, v := range c.queues[in.ChatID] {
			if wanted[v.ID] {
				ids = append(ids, v.ID)
				delete(wanted, v.ID)
				found := false
				for _, a := range doc.Attachments {
					if a.ID == v.ID {
						found = true
						break
					}
				}
				if !found {
					doc.Attachments = append(doc.Attachments, chat.Attachment{ID: v.ID})
					sources = append(sources, history.Source{AttachmentID: v.ID, TempPath: v.Path})
				}
			}
		}
		if len(wanted) != 0 {
			c.mu.Unlock()
			return "", ErrStale
		}
		if len(doc.Messages) == 0 && doc.Title == chat.DefaultTitle {
			title := []rune(strings.Join(strings.Fields(in.Text), " "))
			if len(title) > 80 {
				title = append(title[:79], '…')
			}
			doc.Title = string(title)
			if doc.Title == "" {
				doc.Title = "Задание на снимках"
			}
		}
		turn = chat.Message{Question: questionCard, FromTranscript: origin.kind == originTranscript || origin.kind == originQuestion, ReplyToID: in.ReplyToID, ReplyToKind: in.ReplyToKind, ID: chat.ID(), Role: chat.RoleUser, Text: in.Text, AttachmentIDs: ids, Attempts: []chat.Attempt{}}
	}
	if origin.kind == originComposer && in.RetryMessageID == "" && in.DraftRevision >= c.drafts[in.ChatID].Revision {
		c.drafts[in.ChatID] = Draft{Text: in.Text, Revision: in.DraftRevision, ReplyToID: in.ReplyToID, ReplyToKind: in.ReplyToKind}
	}
	if turn.RequestConfig == nil {
		cfg := doc.Config
		turn.RequestConfig = &cfg
		turn.ConfigRevision = doc.ConfigRevision
	}
	turn.Status = chat.StatusRequesting
	attemptID := chat.ID()
	turn.Attempts = append(turn.Attempts, chat.Attempt{ID: attemptID, Status: chat.StatusRequesting, ConfigRevision: turn.ConfigRevision})
	if turn.RequestConfig.ParallelAnswers {
		cfg := *turn.RequestConfig
		if len(turn.Answers) == 0 {
			turn.Answers = []chat.Answer{
				{Kind: chat.AnswerApproach, Status: chat.StatusRequesting, AttemptID: attemptID, Model: cfg.FastModel, Reasoning: cfg.FastReasoning},
				{Kind: chat.AnswerSolution, Status: chat.StatusRequesting, AttemptID: attemptID, Model: cfg.Model, Reasoning: cfg.Reasoning},
			}
		} else {
			turn.Answers[1] = chat.Answer{Kind: chat.AnswerSolution, Status: chat.StatusRequesting, AttemptID: attemptID, Model: cfg.Model, Reasoning: cfg.Reasoning}
		}
	}
	if in.RetryMessageID != "" {
		doc.Messages[len(doc.Messages)-1] = turn
	} else {
		doc.Messages = append(doc.Messages, turn)
	}
	opID := chat.ID()
	ctx, cancel := context.WithCancel(c.ctx)
	c.op = operation{Operation: Operation{ID: opID, Kind: kindSubmit, Phase: phaseSaving, Message: "Сохраняю вопрос"}, chatID: doc.ID, cancel: cancel}
	if origin.kind == originQuestion {
		c.question.builders[in.ChatID].Sent(turn.ID)
		c.scheduleQuestionLocked()
	}
	c.requests[in.RequestID] = true
	if origin.kind == originComposer && in.RetryMessageID == "" {
		// After a crash this tells whether the saved draft only repeats this question.
		c.submitted[doc.ID] = workstate.Submitted{MessageID: turn.ID, Revision: in.DraftRevision}
		c.statePersist.mark()
	}
	streamChanged := c.clearCheckpointsLocked("")
	if in.RetryMessageID != "" && c.dropRecoveredLocked(func(b workstate.Block) bool { return b.MessageID == in.RetryMessageID }) {
		streamChanged = true
	}
	if streamChanged {
		c.streamPersist.mark()
	}
	c.intent = nil
	c.active = chat.Clone(doc)
	c.err = ""
	c.changedLocked()
	draft := c.drafts[doc.ID]
	clearDraft := origin.kind == originComposer && in.RetryMessageID == "" && draft.Text == in.Text && draft.Revision == in.DraftRevision && draft.ReplyToID == in.ReplyToID && draft.ReplyToKind == in.ReplyToKind
	c.workers.Add(1)
	c.mu.Unlock()
	c.publish()
	go c.runSubmit(ctx, opID, doc, turn, sources, ids, draft, clearDraft)
	return opID, nil
}
func (c *Controller) runSubmit(ctx context.Context, opID string, doc chat.Chat, turn chat.Message, sources []history.Source, ids []string, draft Draft, clearDraft bool) {
	defer c.workers.Done()
	// Persist the submit marker before archiving or sending the question. Even a
	// very fast answer must not outrun the draft debounce and resurrect old text.
	e := c.statePersist.flush()
	var saved chat.Chat
	if e == nil {
		saved, e = c.deps.Store.Save(history.SaveInput{Chat: doc, Sources: sources})
	}
	if e != nil {
		doc.Messages[len(doc.Messages)-1].Status = chat.StatusFailed
		a := len(doc.Messages[len(doc.Messages)-1].Attempts) - 1
		doc.Messages[len(doc.Messages)-1].Attempts[a].Status = chat.StatusFailed
		doc.Messages[len(doc.Messages)-1].Attempts[a].Error = "Вопрос не отправлен: " + e.Error()
		for i := range doc.Messages[len(doc.Messages)-1].Answers {
			answer := &doc.Messages[len(doc.Messages)-1].Answers[i]
			if answer.Status == chat.StatusRequesting {
				answer.Status = chat.StatusFailed
				answer.Error = "Вопрос не отправлен: " + e.Error()
			}
		}
		c.setPending(opID, pendingSave{doc: doc, sources: sources}, e)
		return
	}
	doc = saved
	attachments := map[string]chat.Attachment{}
	for _, a := range doc.Attachments {
		attachments[a.ID] = a
	}
	images := []llm.Image{}
	for _, id := range ids {
		p, err := c.deps.Store.AttachmentPath(doc.ID, attachments[id])
		if err != nil {
			e = err
			break
		}
		images = append(images, llm.Image{ID: id, Path: p})
	}
	c.mu.Lock()
	if c.op.ID != opID {
		c.mu.Unlock()
		return
	}
	c.active = chat.Clone(doc)
	c.rememberLocked(doc)
	c.op.Phase = phaseRequesting
	c.op.Message = "Ожидаю ответ"
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	if turn.RequestConfig != nil && turn.RequestConfig.ParallelAnswers {
		c.runParallel(ctx, opID, doc, images, ids, draft, clearDraft, e)
		return
	}
	var result llm.Result
	attemptID := turn.Attempts[len(turn.Attempts)-1].ID
	if e == nil {
		req := llm.Request{
			ChatID:             doc.ID,
			GlobalInstructions: doc.GlobalInstructions,
			History:            append([]chat.Message(nil), doc.Messages[:len(doc.Messages)-1]...),
			Turn:               turn,
			Config:             *turn.RequestConfig,
			ConfigRevision:     turn.ConfigRevision,
			State:              doc.ProviderState,
			Images:             images,
		}
		result, e = c.deps.Provider.Generate(ctx, req, c.streamSink(opID, doc.ID, turn.ID, attemptID, blockMessage))
	}
	c.closeStream(opID, attemptID, blockMessage)
	if len(result.State) > 0 {
		doc.ProviderState = result.State
	}
	last := len(doc.Messages) - 1
	attempt := len(doc.Messages[last].Attempts) - 1
	status := chat.StatusComplete
	if e != nil {
		status = chat.StatusFailed
		if errors.Is(e, llm.ErrOutcomeUnknown) {
			status = chat.StatusUnknown
		} else if errors.Is(e, context.Canceled) {
			status = chat.StatusCanceled
		}
		doc.Messages[last].Attempts[attempt].Error = e.Error()
	}
	doc.Messages[last].Status = status
	doc.Messages[last].Attempts[attempt].Status = status
	if e == nil {
		doc.Messages = append(doc.Messages, chat.Message{ID: chat.ID(), Role: chat.RoleAssistant, Text: result.Text, Status: chat.StatusComplete, AttachmentIDs: []string{}, Attempts: []chat.Attempt{}, ConfigRevision: turn.ConfigRevision})
	}
	c.mu.Lock()
	if c.op.ID != opID {
		c.mu.Unlock()
		return
	}
	c.active = chat.Clone(doc)
	c.op.Phase = phaseSaving
	c.op.Message = "Сохраняю результат"
	if e != nil {
		c.err = e.Error()
	}
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	p := pendingSave{doc: doc, draft: draft, clearDraft: clearDraft && e == nil}
	if e == nil {
		p.cleanupIDs = ids
	}
	saved, saveErr := c.timedSave(history.SaveInput{Chat: doc})
	if saveErr != nil {
		c.setPending(opID, p, saveErr)
		return
	}
	c.finishSave(opID, saved, p)
}

// timedSave records the content-free archive time of a received answer.
func (c *Controller) timedSave(in history.SaveInput) (chat.Chat, error) {
	started := time.Now()
	saved, err := c.deps.Store.Save(in)
	c.appendLog(fmt.Sprintf("Solveo timing: save_ms=%d ok=%t\n", time.Since(started).Milliseconds(), err == nil))
	return saved, err
}
func (c *Controller) setPending(opID string, p pendingSave, e error) {
	c.mu.Lock()
	if c.op.ID != opID {
		c.mu.Unlock()
		return
	}
	c.active = chat.Clone(p.doc)
	c.pending = &p
	c.op.Phase = phaseSavePending
	c.op.Message = "Результат пока не сохранён"
	c.err = e.Error()
	if c.op.cancel != nil {
		c.op.cancel()
		c.op.cancel = nil
	}
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
}
func (c *Controller) finishSave(opID string, saved chat.Chat, p pendingSave) {
	c.mu.Lock()
	if c.op.ID != opID {
		c.mu.Unlock()
		return
	}
	c.active = chat.Clone(saved)
	c.rememberLocked(saved)
	c.pending = nil
	paths := []string{}
	queued := map[string]string{}
	if p.cleanupIDs != nil {
		for _, q := range c.queues[saved.ID] {
			queued[q.Path] = q.ID
		}
		paths = c.cleanupQueueLocked(saved.ID, p.cleanupIDs)
	}
	if p.clearDraft && c.drafts[saved.ID] == p.draft {
		d := c.drafts[saved.ID]
		d.Text = ""
		d.ReplyToID = ""
		d.ReplyToKind = ""
		d.Revision++
		c.drafts[saved.ID] = d
	}
	// The archive now holds the outcome: the submit marker and checkpoints are obsolete.
	delete(c.submitted, saved.ID)
	c.statePersist.mark()
	if c.clearCheckpointsLocked(opID) {
		c.streamPersist.mark()
	}
	if c.op.cancel != nil {
		c.op.cancel()
	}
	c.op = operation{Operation: Operation{Phase: phaseIdle}}
	c.changedLocked()
	c.mu.Unlock()
	if len(paths) > 0 && c.deps.NativeHost != nil {
		archived := map[string]chat.Attachment{}
		for _, a := range saved.Attachments {
			archived[a.ID] = a
		}
		remap := map[string]string{}
		for _, q := range paths {
			if a, ok := archived[queued[q]]; ok {
				if to, e := c.deps.Store.AttachmentPath(saved.ID, a); e == nil {
					remap[q] = to
				}
			}
		}
		if err := c.deps.NativeHost.RebindImages(remap); err != nil {
			c.ReportError(fmt.Errorf("не удалось обновить просмотр снимков, временные PNG останутся до выхода: %w", err))
			c.publish()
			return
		}
	}
	c.removeQueueFiles(paths)
	c.publish()
}
func (c *Controller) RetrySave() error {
	c.mu.Lock()
	if c.pending == nil {
		c.mu.Unlock()
		return nil
	}
	if c.op.Phase != phaseSavePending {
		c.mu.Unlock()
		return ErrBusy
	}
	p := *c.pending
	id := c.op.ID
	c.op.Phase = phaseSaving
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	saved, e := c.deps.Store.Save(history.SaveInput{Chat: p.doc, Sources: p.sources})
	if e != nil {
		c.setPending(id, p, e)
		return e
	}
	c.finishSave(id, saved, p)
	return nil
}
func (c *Controller) DiscardUnsaved() (Snapshot, error) {
	c.mu.Lock()
	if c.pending == nil {
		c.mu.Unlock()
		return c.Snapshot(), nil
	}
	if c.op.Phase != phaseSavePending {
		c.mu.Unlock()
		return Snapshot{}, ErrBusy
	}
	c.pauseQuestionLocked()
	oldID := c.active.ID
	doc := newChat(c.settings.GlobalInstructions)
	c.pending = nil
	c.intent = nil
	c.active = chat.Clone(doc)
	c.queues[doc.ID] = c.queues[oldID]
	delete(c.queues, oldID)
	draft := c.drafts[oldID]
	// A new chat cannot reference an answer from the discarded conversation.
	if draft.ReplyToID != "" {
		draft.ReplyToID, draft.ReplyToKind = "", ""
		draft.Revision++
	}
	c.drafts[doc.ID] = draft
	delete(c.drafts, oldID)
	// The discarded question stays in the old chat's archive; keep the draft rather
	// than clear it as a duplicate after a crash.
	delete(c.submitted, oldID)
	c.statePersist.mark()
	c.op = operation{Operation: Operation{ID: chat.ID(), Kind: kindDiscard, Phase: phaseSaving}}
	c.err = "Несохранённый ответ отброшен. Снимки перенесены в новый чат"
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	// Persist the replacement chat so it appears in history and its moved queue stays
	// reachable after switching chats. The archive may still be failing: then the chat
	// stays in memory only and the queue remains usable until exit.
	saved, e := c.deps.Store.Save(history.SaveInput{Chat: doc})
	c.mu.Lock()
	if e == nil {
		c.active = chat.Clone(saved)
		c.rememberLocked(saved)
	} else {
		c.err += ". Новый чат пока не сохранён в историю: " + e.Error()
	}
	c.op = operation{Operation: Operation{Phase: phaseIdle}}
	c.prepareChatLocked()
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	return c.Snapshot(), nil
}
func (c *Controller) Cancel(id string) error {
	c.mu.Lock()
	if c.op.ID != id || c.op.Phase != phaseRequesting || c.op.cancel == nil {
		c.mu.Unlock()
		return ErrStale
	}
	cancel := c.op.cancel
	c.mu.Unlock()
	cancel()
	return nil
}
func (c *Controller) CheckConnection() (string, error) {
	c.mu.Lock()
	if e := c.availableLocked(); e != nil {
		c.mu.Unlock()
		return "", e
	}
	id := chat.ID()
	cfg := c.active.Config
	ctx, cancel := context.WithCancel(c.ctx)
	c.op = operation{Operation: Operation{ID: id, Kind: kindCheck, Phase: phaseRequesting, Message: "Проверяю соединение"}, cancel: cancel}
	c.changedLocked()
	c.workers.Add(1)
	c.mu.Unlock()
	c.publish()
	go func() {
		defer c.workers.Done()
		defer cancel()
		out, e := c.deps.Provider.Check(ctx, cfg, func(v llm.Event) {
			if v.Kind == llm.EventProgress {
				c.appendLog(v.Text)
			}
		})
		c.mu.Lock()
		if c.op.ID != id {
			c.mu.Unlock()
			return
		}
		if e != nil {
			c.err = e.Error()
		} else {
			c.err = ""
		}
		c.op = operation{Operation: Operation{Phase: phaseIdle, Message: out.Text}}
		c.prepareChatLocked()
		c.changedLocked()
		c.mu.Unlock()
		c.appendLog(fmt.Sprintf("Connection check: %s\n", out.Text))
		c.publish()
	}()
	return id, nil
}
func (c *Controller) cleanupQueueLocked(id string, ids []string) []string {
	all := ids == nil
	set := map[string]bool{}
	for _, v := range ids {
		set[v] = true
	}
	keep := []queued{}
	paths := []string{}
	for _, v := range c.queues[id] {
		if all || set[v.ID] {
			paths = append(paths, v.Path)
		} else {
			keep = append(keep, v)
		}
	}
	c.queues[id] = keep
	return paths
}

// removeFiles deletes removed queue images after the viewer releases them. If the
// viewer cannot close, the files stay unreferenced and the next start prunes them.
func (c *Controller) removeFiles(paths []string) {
	if len(paths) == 0 {
		return
	}
	if c.deps.NativeHost != nil {
		if err := c.deps.NativeHost.CloseImages(); err != nil {
			c.saveWorkNow()
			c.ReportError(err)
			return
		}
	}
	c.removeQueueFiles(paths)
}

// logFlushInterval coalesces CLI output: the native log edit control is rewritten
// with up to 64 KiB of text on each update, so per-chunk updates are expensive.
const logFlushInterval = 200 * time.Millisecond

func (c *Controller) appendLog(text string) {
	if text == "" {
		return
	}
	c.mu.Lock()
	c.logs += text
	if len(c.logs) > 64<<10 {
		c.logs = c.logs[len(c.logs)-(64<<10):]
	}
	// The log lives only in the native log window. Publishing the whole snapshot
	// for every CLI output chunk would re-render the chat for no visible change.
	schedule := c.deps.NativeHost != nil && !c.logFlushPending
	if schedule {
		c.logFlushPending = true
	}
	c.mu.Unlock()
	if schedule {
		time.AfterFunc(logFlushInterval, c.flushLog)
	}
}
func (c *Controller) flushLog() {
	c.mu.Lock()
	c.logFlushPending = false
	log := c.logs
	c.mu.Unlock()
	_ = c.deps.NativeHost.SetLog(log)
}
