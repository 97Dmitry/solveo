package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
	"os"
	"strings"
)

func (c *Controller) Submit(in SubmitInput) (string, error) { return c.submit(in, "") }
func (c *Controller) submit(in SubmitInput, captureID string) (string, error) {
	c.mu.Lock()
	if e := c.availableLocked(); e != nil && (captureID == "" || c.op.ID != captureID || c.op.Phase != "capturing") {
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
		return "", errors.New("provider is unavailable for this archive")
	}
	if in.IntentID != "" {
		if c.intent == nil || in.IntentID != c.intent.ID || in.ChatID != c.intent.ChatID || c.deps.Now().UnixMilli() >= c.intent.ExpiresAt || in.DraftRevision < c.intent.DraftRevision {
			c.mu.Unlock()
			return "", ErrStale
		}
	}
	if captureID == "" && in.RetryMessageID == "" && in.DraftRevision >= c.drafts[in.ChatID].Revision {
		c.drafts[in.ChatID] = Draft{Text: in.Text, Revision: in.DraftRevision}
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
		if last.ID != in.RetryMessageID || last.Role != "user" || last.Status == "complete" {
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
			return "", errors.New("question exceeds 1 MiB")
		}
		wanted := map[string]bool{}
		for _, id := range in.AttachmentIDs {
			if wanted[id] {
				c.mu.Unlock()
				return "", errors.New("duplicate attachment")
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
		if len(doc.Messages) == 0 && doc.Title == "Новый чат" {
			title := []rune(strings.Join(strings.Fields(in.Text), " "))
			if len(title) > 80 {
				title = append(title[:79], '…')
			}
			doc.Title = string(title)
			if doc.Title == "" {
				doc.Title = "Задание на снимках"
			}
		}
		turn = chat.Message{ID: chat.ID(), Role: "user", Text: in.Text, AttachmentIDs: ids, Attempts: []chat.Attempt{}}
	}
	turn.Status = "requesting"
	turn.ConfigRevision = doc.ConfigRevision
	turn.Attempts = append(turn.Attempts, chat.Attempt{ID: chat.ID(), Status: "requesting", ConfigRevision: doc.ConfigRevision})
	if in.RetryMessageID != "" {
		doc.Messages[len(doc.Messages)-1] = turn
	} else {
		doc.Messages = append(doc.Messages, turn)
	}
	opID := chat.ID()
	ctx, cancel := context.WithCancel(c.ctx)
	c.op = operation{Operation: Operation{ID: opID, Kind: "submit", Phase: "saving", Message: "Сохраняю вопрос"}, chatID: doc.ID, cancel: cancel}
	c.requests[in.RequestID] = true
	c.intent = nil
	c.active = chat.Clone(doc)
	c.err = ""
	c.changedLocked()
	draft := c.drafts[doc.ID]
	clearDraft := in.RetryMessageID == "" && draft.Text == in.Text && draft.Revision == in.DraftRevision
	c.workers.Add(1)
	c.mu.Unlock()
	c.publish()
	go c.runSubmit(ctx, opID, doc, turn, sources, ids, draft, clearDraft)
	return opID, nil
}
func (c *Controller) runSubmit(ctx context.Context, opID string, doc chat.Chat, turn chat.Message, sources []history.Source, ids []string, draft Draft, clearDraft bool) {
	defer c.workers.Done()
	saved, e := c.deps.Store.Save(history.SaveInput{Chat: doc, Sources: sources})
	if e != nil {
		doc.Messages[len(doc.Messages)-1].Status = "failed"
		a := len(doc.Messages[len(doc.Messages)-1].Attempts) - 1
		doc.Messages[len(doc.Messages)-1].Attempts[a].Status = "failed"
		doc.Messages[len(doc.Messages)-1].Attempts[a].Error = "Question was not sent: " + e.Error()
		c.setPending(opID, pendingSave{doc: doc, sources: sources}, e)
		return
	}
	doc = saved
	images := []llm.Image{}
	for _, id := range ids {
		p, err := c.deps.Store.ImagePath(doc.ID, id)
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
	c.op.Phase = "requesting"
	c.op.Message = "Ожидаю ответ"
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	var result llm.Result
	if e == nil {
		result, e = c.deps.Provider.Generate(ctx, llm.Request{History: append([]chat.Message(nil), doc.Messages[:len(doc.Messages)-1]...), Turn: turn, Config: doc.Config, ConfigRevision: doc.ConfigRevision, State: doc.ProviderState, Images: images}, func(v llm.Event) { c.appendLog(v.Text) })
	}
	if len(result.State) > 0 {
		doc.ProviderState = result.State
	}
	last := len(doc.Messages) - 1
	attempt := len(doc.Messages[last].Attempts) - 1
	status := "complete"
	if e != nil {
		status = "failed"
		if errors.Is(e, llm.ErrOutcomeUnknown) {
			status = "unknown"
		} else if errors.Is(e, context.Canceled) {
			status = "canceled"
		}
		doc.Messages[last].Attempts[attempt].Error = e.Error()
	}
	doc.Messages[last].Status = status
	doc.Messages[last].Attempts[attempt].Status = status
	if e == nil {
		doc.Messages = append(doc.Messages, chat.Message{ID: chat.ID(), Role: "assistant", Text: result.Text, Status: "complete", AttachmentIDs: []string{}, Attempts: []chat.Attempt{}, ConfigRevision: doc.ConfigRevision})
	}
	c.mu.Lock()
	if c.op.ID != opID {
		c.mu.Unlock()
		return
	}
	c.active = chat.Clone(doc)
	c.op.Phase = "saving"
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
	saved, saveErr := c.deps.Store.Save(history.SaveInput{Chat: doc})
	if saveErr != nil {
		c.setPending(opID, p, saveErr)
		return
	}
	c.finishSave(opID, saved, p)
}
func (c *Controller) setPending(opID string, p pendingSave, e error) {
	c.mu.Lock()
	if c.op.ID != opID {
		c.mu.Unlock()
		return
	}
	c.active = chat.Clone(p.doc)
	c.pending = &p
	c.op.Phase = "save-pending"
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
	if p.cleanupIDs != nil {
		paths = c.cleanupQueueLocked(saved.ID, p.cleanupIDs)
	}
	if p.clearDraft && c.drafts[saved.ID] == p.draft {
		d := c.drafts[saved.ID]
		d.Text = ""
		d.Revision++
		c.drafts[saved.ID] = d
	}
	if c.op.cancel != nil {
		c.op.cancel()
	}
	c.op = operation{Operation: Operation{Phase: "idle"}}
	c.changedLocked()
	c.mu.Unlock()
	if len(paths) > 0 && c.deps.NativeHost != nil {
		remap := map[string]string{}
		for _, q := range paths {
			for _, a := range saved.Attachments {
				if strings.HasSuffix(q, a.ID+".png") {
					if to, e := c.deps.Store.ImagePath(saved.ID, a.ID); e == nil {
						remap[q] = to
					}
				}
			}
		}
		if err := c.deps.NativeHost.RebindImages(remap); err != nil {
			c.ReportError(fmt.Errorf("rebind image viewer; temporary PNGs retained until exit: %w", err))
			c.publish()
			return
		}
	}
	for _, p := range paths {
		_ = os.Remove(p)
	}
	c.publish()
}
func (c *Controller) RetrySave() error {
	c.mu.Lock()
	if c.pending == nil {
		c.mu.Unlock()
		return nil
	}
	if c.op.Phase != "save-pending" {
		c.mu.Unlock()
		return ErrBusy
	}
	p := *c.pending
	id := c.op.ID
	c.op.Phase = "saving"
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
	if c.op.Phase != "save-pending" {
		c.mu.Unlock()
		return Snapshot{}, ErrBusy
	}
	oldID := c.active.ID
	c.pending = nil
	c.intent = nil
	c.active = chat.New()
	c.queues[c.active.ID] = c.queues[oldID]
	delete(c.queues, oldID)
	c.drafts[c.active.ID] = c.drafts[oldID]
	c.op = operation{Operation: Operation{Phase: "idle"}}
	c.err = "Несохранённый ответ отброшен. Снимки перенесены в новый чат"
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	return c.Snapshot(), nil
}
func (c *Controller) Cancel(id string) error {
	c.mu.Lock()
	if c.op.ID != id || c.op.Phase != "requesting" || c.op.cancel == nil {
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
	c.op = operation{Operation: Operation{ID: id, Kind: "check", Phase: "requesting", Message: "Проверяю соединение"}, cancel: cancel}
	c.changedLocked()
	c.workers.Add(1)
	c.mu.Unlock()
	c.publish()
	go func() {
		defer c.workers.Done()
		defer cancel()
		out, e := c.deps.Provider.Check(ctx, cfg, func(v llm.Event) { c.appendLog(v.Text) })
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
		c.op = operation{Operation: Operation{Phase: "idle", Message: out.Text}}
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
func (c *Controller) removeFiles(paths []string) {
	if len(paths) > 0 && c.deps.NativeHost != nil {
		if err := c.deps.NativeHost.CloseImages(); err != nil {
			c.ReportError(err)
			return
		}
	}
	for _, p := range paths {
		_ = os.Remove(p)
	}
}
func (c *Controller) appendLog(text string) {
	if text == "" {
		return
	}
	c.mu.Lock()
	c.logs += text
	if len(c.logs) > 64<<10 {
		c.logs = c.logs[len(c.logs)-(64<<10):]
	}
	log := c.logs
	c.changedLocked()
	c.mu.Unlock()
	if c.deps.NativeHost != nil {
		_ = c.deps.NativeHost.SetLog(log)
	}
	c.publish()
}
