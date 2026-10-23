package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/workstate"
)

const (
	maxDeleteChats = 500
	maxSearchHits  = 50
)

var errSearchReplaced = errors.New("поиск заменён более новым запросом")

// DeleteChats removes chats with their images, drafts and queues. When the
// active chat is selected, the newest remaining chat (or a new one) is opened
// first. Work state without the selected chats is written before any archive
// deletion, so a crash cannot bring their work back as orphans.
func (c *Controller) DeleteChats(ids []string) (DeleteResult, error) {
	result := DeleteResult{Deleted: []string{}, Failed: []DeleteFailure{}}
	if len(ids) == 0 || len(ids) > maxDeleteChats {
		return result, fmt.Errorf("выберите от 1 до %d чатов", maxDeleteChats)
	}
	c.mu.Lock()
	if e := c.availableLocked(); e != nil {
		c.mu.Unlock()
		return result, e
	}
	selected := map[string]bool{}
	for _, id := range ids {
		if selected[id] {
			c.mu.Unlock()
			return result, errors.New("чат выбран повторно")
		}
		selected[id] = true
	}
	titles := map[string]string{}
	next := ""
	for _, s := range c.summaries {
		if selected[s.ID] {
			titles[s.ID] = s.Title
		} else if next == "" {
			next = s.ID
		}
	}
	if len(titles) != len(selected) {
		c.mu.Unlock()
		return result, ErrStale
	}
	switching := selected[c.active.ID]
	if switching && c.preparationCancel != nil {
		c.preparationCancel()
		c.preparationCancel = nil
	}
	if switching {
		c.intent = nil
	}
	c.op = operation{Operation: Operation{ID: chat.ID(), Kind: kindDelete, Phase: phaseSaving, Message: "Удаляю чаты"}}
	instructions := c.settings.GlobalInstructions
	c.changedLocked()
	c.mu.Unlock()
	c.publish()

	if switching && c.deps.NativeHost != nil {
		// The viewer shows only the active chat; it must release those images first.
		if e := c.deps.NativeHost.CloseImages(); e != nil {
			c.mu.Lock()
			c.op = operation{Operation: Operation{Phase: phaseIdle}}
			c.prepareChatLocked()
			c.changedLocked()
			c.mu.Unlock()
			c.publish()
			return result, fmt.Errorf("не удалось закрыть просмотр снимков: %w", e)
		}
	}
	if switching {
		c.mu.Lock()
		c.pauseQuestionLocked()
		c.mu.Unlock()
		doc, e := chat.Chat{}, errors.New("нет других чатов")
		if next != "" {
			doc, e = c.deps.Store.Load(next)
		}
		if e != nil {
			doc, e = c.deps.Store.Save(history.SaveInput{Chat: newChat(instructions)})
		}
		c.mu.Lock()
		if e != nil {
			c.op = operation{Operation: Operation{Phase: phaseIdle}}
			c.prepareChatLocked()
			c.changedLocked()
			c.mu.Unlock()
			c.publish()
			return result, fmt.Errorf("не удалось открыть другой чат перед удалением: %w", e)
		}
		c.active = chat.Clone(doc)
		c.rememberLocked(doc)
		c.changedLocked()
		c.mu.Unlock()
		c.publish()
	}

	type work struct {
		queue    []queued
		draft    Draft
		hadDraft bool
	}
	saved := map[string]work{}
	c.mu.Lock()
	for id := range selected {
		draft, hadDraft := c.drafts[id]
		saved[id] = work{c.queues[id], draft, hadDraft}
		delete(c.queues, id)
		delete(c.drafts, id)
		delete(c.submitted, id)
	}
	if c.dropRecoveredLocked(func(b workstate.Block) bool { return selected[b.ChatID] }) {
		c.streamPersist.mark()
	}
	c.mu.Unlock()
	c.statePersist.mark()
	flushErr := c.statePersist.flush()
	failed := map[string]bool{}
	for _, id := range ids {
		e := flushErr
		if e == nil {
			e = c.deps.Store.Delete(id)
		}
		if e != nil {
			failed[id] = true
			result.Failed = append(result.Failed, DeleteFailure{ID: id, Title: titles[id], Error: e.Error()})
			continue
		}
		result.Deleted = append(result.Deleted, id)
	}
	c.mu.Lock()
	for _, id := range result.Deleted {
		delete(c.question.builders, id)
	}
	kept := c.summaries[:0]
	for _, s := range c.summaries {
		if !selected[s.ID] || failed[s.ID] {
			kept = append(kept, s)
		}
	}
	c.summaries = kept
	removed := []string{}
	for id, w := range saved {
		if failed[id] {
			c.queues[id] = w.queue
			if w.hadDraft {
				c.drafts[id] = w.draft
			}
			continue
		}
		for _, q := range w.queue {
			removed = append(removed, q.Path)
		}
	}
	if len(failed) > 0 {
		c.statePersist.mark()
	}
	c.op = operation{Operation: Operation{Phase: phaseIdle}}
	if switching {
		c.prepareChatLocked()
	}
	c.changedLocked()
	c.mu.Unlock()
	// Queued PNGs of deleted chats are no longer referenced by the written state.
	for _, p := range removed {
		_ = os.Remove(p)
	}
	c.publish()
	return result, nil
}

// SetChatPinned changes only pins metadata, so it does not need the operation slot.
func (c *Controller) SetChatPinned(id string, pinned bool) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("приложение завершается")
	}
	known := false
	for _, s := range c.summaries {
		known = known || s.ID == id
	}
	c.mu.Unlock()
	if !known {
		return ErrStale
	}
	if e := c.deps.Store.SetPinned(id, pinned); e != nil {
		return fmt.Errorf("не удалось изменить закрепление: %w", e)
	}
	c.mu.Lock()
	for i := range c.summaries {
		if c.summaries[i].ID == id {
			c.summaries[i].Pinned = pinned
		}
	}
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	return nil
}

// SearchHistory scans the archive outside the operation slot. A newer query
// cancels the previous scan; Close waits for running scans.
func (c *Controller) SearchHistory(query string) ([]SearchHit, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("приложение завершается")
	}
	if c.searchCancel != nil {
		c.searchCancel()
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.searchCancel = cancel
	c.workers.Add(1)
	c.mu.Unlock()
	defer c.workers.Done()
	defer cancel()
	hits, e := c.deps.Store.Search(ctx, query, maxSearchHits)
	if ctx.Err() != nil {
		return nil, errSearchReplaced
	}
	if e != nil {
		return nil, e
	}
	out := make([]SearchHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, SearchHit{ChatID: h.ChatID, Title: h.Title, Snippet: h.Snippet, Matches: h.Matches, Pinned: h.Pinned, UpdatedAt: h.UpdatedAt.UTC().Format(time.RFC3339Nano)})
	}
	return out, nil
}

// ExportChat writes a saved chat with its images to solveo-data/exports.
func (c *Controller) ExportChat(id string) (history.ExportResult, error) {
	c.mu.Lock()
	if e := c.availableLocked(); e != nil {
		c.mu.Unlock()
		return history.ExportResult{}, e
	}
	c.op = operation{Operation: Operation{ID: chat.ID(), Kind: kindExport, Phase: phaseSaving, Message: "Экспортирую чат"}}
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	out, e := c.deps.Store.Export(id, c.deps.Now())
	c.mu.Lock()
	c.op = operation{Operation: Operation{Phase: phaseIdle}}
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	if e != nil {
		return out, fmt.Errorf("не удалось экспортировать чат: %w", e)
	}
	return out, nil
}

// RevealExport opens an export folder in Explorer. The renderer names the
// folder only; the path is resolved inside the archive.
func (c *Controller) RevealExport(name string) error {
	p, e := c.deps.Store.ExportPath(name)
	if e != nil {
		return e
	}
	if c.deps.NativeHost == nil {
		return errors.New("открытие папок недоступно")
	}
	return c.deps.NativeHost.OpenFolder(p)
}
