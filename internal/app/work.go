package app

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/workstate"
)

// Draft edits are coalesced; answer checkpoints are written at most once per interval.
const (
	stateSaveDelay  = 300 * time.Millisecond
	streamSaveDelay = time.Second
	workRetryDelay  = 2 * time.Second
)

// persister writes one work file. Each write serializes the controller's state at
// write time, and writes run one at a time in that order, so a delayed write can
// never replace newer content with older.
type persister struct {
	delay   time.Duration
	collect func() any
	save    func(any) error
	report  func(error)

	writeMu sync.Mutex
	mu      sync.Mutex
	dirty   bool
	timed   bool
	stopped bool
	paused  bool // construction may mark dirty, but must not publish or write yet
}

// mark records a change and schedules its write. It may be called under Controller.mu.
func (p *persister) mark() {
	p.mu.Lock()
	p.dirty = true
	p.scheduleLocked(p.delay)
	p.mu.Unlock()
}
func (p *persister) scheduleLocked(d time.Duration) {
	if p.timed || p.stopped || p.paused {
		return
	}
	p.timed = true
	time.AfterFunc(d, func() { _ = p.flush() })
}

// resume enables background writes after the controller is fully initialized.
// Protected read-only state remains stopped; dirty recovery changes are retained.
func (p *persister) resume() {
	p.mu.Lock()
	p.paused = false
	if p.dirty {
		p.scheduleLocked(p.delay)
	}
	p.mu.Unlock()
}

// flush writes pending changes now. Never call it while holding Controller.mu.
func (p *persister) flush() error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	p.mu.Lock()
	p.timed = false
	if !p.dirty || p.stopped || p.paused {
		p.mu.Unlock()
		return nil
	}
	p.dirty = false
	p.mu.Unlock()
	err := p.save(p.collect())
	if err != nil {
		p.mu.Lock()
		p.dirty = true
		p.scheduleLocked(workRetryDelay)
		p.mu.Unlock()
	}
	p.report(err)
	return err
}

// stop waits for a running write and disables further writes.
func (p *persister) stop() {
	p.writeMu.Lock()
	p.mu.Lock()
	p.stopped = true
	p.mu.Unlock()
	p.writeMu.Unlock()
}

func (c *Controller) initPersisters() {
	c.statePersist = &persister{
		paused: true,
		delay:  stateSaveDelay,
		collect: func() any {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.workStateLocked()
		},
		save: func(v any) error {
			started := time.Now()
			err := c.deps.Work.Save(v.(workstate.State))
			if err != nil || time.Since(started) >= 100*time.Millisecond {
				c.appendLog(fmt.Sprintf("Solveo timing: work_save_ms=%d file=state ok=%t\n", time.Since(started).Milliseconds(), err == nil))
			}
			return err
		},
		report: func(err error) {
			c.reportWork(&c.stateErr, "Черновики и очередь не сохранены на диск: ", err)
		},
	}
	c.streamPersist = &persister{
		paused: true,
		delay:  streamSaveDelay,
		collect: func() any {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.streamBlocksLocked()
		},
		save: func(v any) error {
			started := time.Now()
			err := c.deps.Work.SaveStream(v.([]workstate.Block))
			c.appendLog(fmt.Sprintf("Solveo timing: work_save_ms=%d file=stream ok=%t\n", time.Since(started).Milliseconds(), err == nil))
			return err
		},
		report: func(err error) {
			c.reportWork(&c.streamErr, "Части ответа не сохранены на диск: ", err)
		},
	}
}

func (c *Controller) reportWork(field *string, prefix string, err error) {
	text := ""
	if err != nil {
		text = prefix + err.Error()
	}
	c.mu.Lock()
	changed := *field != text
	*field = text
	if changed {
		c.changedLocked()
	}
	c.mu.Unlock()
	if changed {
		c.publish()
	}
}

// saveWorkNow persists queue changes before their files are created or removed.
// A failure is already visible as the work error; the in-memory state stays usable.
func (c *Controller) saveWorkNow() {
	c.statePersist.mark()
	_ = c.statePersist.flush()
}

func (c *Controller) workStateLocked() workstate.State {
	st := workstate.State{Chats: map[string]workstate.Chat{}}
	ids := map[string]bool{}
	for id := range c.drafts {
		ids[id] = true
	}
	for id := range c.queues {
		ids[id] = true
	}
	for id := range c.submitted {
		ids[id] = true
	}
	for id := range ids {
		w := workstate.Chat{Draft: workstate.Draft(c.drafts[id]), Queue: []workstate.Item{}}
		for _, q := range c.queues[id] {
			w.Queue = append(w.Queue, workstate.Item{ID: q.ID, Label: q.Label})
		}
		if s, ok := c.submitted[id]; ok {
			w.Submitted = &s
		}
		if w.Draft.Text == "" && w.Draft.ReplyToID == "" && len(w.Queue) == 0 && w.Submitted == nil {
			continue
		}
		st.Chats[id] = w
	}
	return st
}

// streamBlocksLocked returns blocks recovered at startup followed by the newest
// checkpoints of the current operation.
func (c *Controller) streamBlocksLocked() []workstate.Block {
	out := append([]workstate.Block{}, c.recovered...)
	keys := make([]streamKey, 0, len(c.checkpoints))
	for k := range c.checkpoints {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].block < keys[j].block })
	for _, k := range keys {
		out = append(out, c.checkpoints[k])
	}
	if len(out) > workstate.MaxBlocks {
		out = out[len(out)-workstate.MaxBlocks:]
	}
	return out
}

// clearCheckpointsLocked drops answer checkpoints of finished operations and
// reports whether the stream file must be rewritten.
func (c *Controller) clearCheckpointsLocked(opID string) bool {
	changed := false
	for k := range c.checkpoints {
		if opID == "" || k.opID == opID {
			delete(c.checkpoints, k)
			changed = true
		}
	}
	return changed
}

// dropRecoveredLocked forgets recovered text of a retried message or deleted chat.
func (c *Controller) dropRecoveredLocked(match func(workstate.Block) bool) bool {
	keep := c.recovered[:0]
	for _, b := range c.recovered {
		if !match(b) {
			keep = append(keep, b)
		}
	}
	changed := len(keep) != len(c.recovered)
	c.recovered = keep
	return changed
}

// recoveredLocked returns text received before a crash for interrupted attempts
// of the active chat. It is shown only; it never becomes an answer or LLM context.
func (c *Controller) recoveredLocked(doc chat.Chat) []StreamBlock {
	out := []StreamBlock{}
	for _, b := range c.recovered {
		if b.ChatID != doc.ID || !interruptedBlock(doc, b) {
			continue
		}
		out = append(out, StreamBlock{ChatID: b.ChatID, MessageID: b.MessageID, AttemptID: b.AttemptID, Block: b.Block, Text: b.Text})
	}
	return out
}

func interruptedBlock(doc chat.Chat, b workstate.Block) bool {
	for _, m := range doc.Messages {
		if m.ID != b.MessageID || m.Role != chat.RoleUser {
			continue
		}
		if b.Block == blockMessage {
			n := len(m.Attempts)
			return len(m.Answers) == 0 && m.Status == chat.StatusInterrupted && n > 0 && m.Attempts[n-1].ID == b.AttemptID
		}
		for _, a := range m.Answers {
			if a.Kind == b.Block && a.AttemptID == b.AttemptID {
				return a.Status == chat.StatusInterrupted
			}
		}
	}
	return false
}

// restoreWork loads unfinished work after a start. It never submits anything and
// returns notices for the user.
func (c *Controller) restoreWork() []string {
	var notices []string
	st, notice, err := c.deps.Work.Load()
	if notice != "" {
		notices = append(notices, notice)
	}
	if err != nil {
		// Never overwrite work that cannot be read or was written by a newer version.
		c.statePersist.stopped = true
		c.stateErr = "Черновики и очередь не сохраняются: " + err.Error()
		return notices
	}
	// Preflight before adopting chats, rewriting state or pruning PNGs. A failed
	// probe is not evidence that a file was deleted. Preserve the entire durable
	// state until a later restart can read the queue successfully.
	for _, w := range st.Chats {
		for _, item := range w.Queue {
			if _, e := c.deps.Work.Has(item.ID); e != nil {
				c.statePersist.stopped = true
				c.stateErr = "Очередь не прочитана; рабочие файлы сохранены без изменений. Восстановите доступ и перезапустите Solveo: " + e.Error()
				return notices
			}
		}
	}
	exists := func(id string) bool {
		ok, e := c.deps.Store.Exists(id)
		return ok || e != nil
	}
	changed, missing, adopted := false, 0, 0
	keep := map[string]bool{}
	ids := make([]string, 0, len(st.Chats))
	for id := range st.Chats {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		w := st.Chats[id]
		queue := []queued{}
		for _, item := range w.Queue {
			p, e := c.deps.Work.Path(item.ID)
			present, probeErr := c.deps.Work.Has(item.ID)
			if probeErr != nil {
				c.statePersist.stopped = true
				c.stateErr = "Очередь не прочитана; рабочие файлы сохранены без изменений: " + probeErr.Error()
				return notices
			}
			if e != nil || !present {
				missing++
				changed = true
				continue
			}
			keep[item.ID] = true
			queue = append(queue, queued{ID: item.ID, Path: p, Label: item.Label})
		}
		draft := Draft(w.Draft)
		if w.Submitted != nil {
			// The submitted operation ended with the process; the archive now holds its outcome.
			changed = true
			if draft.Text != "" && draft.Revision == w.Submitted.Revision {
				if doc, e := c.deps.Store.Load(id); e == nil && hasMessage(doc, w.Submitted.MessageID) {
					draft = Draft{Revision: draft.Revision + 1}
				}
			}
		}
		if !exists(id) {
			changed = true
			if draft.Text == "" && len(queue) == 0 {
				continue
			}
			// Work of a chat missing from history (a replacement chat that could not be
			// archived) moves to a new chat rather than being lost.
			doc, e := c.deps.Store.Save(history.SaveInput{Chat: newChat(c.settings.GlobalInstructions)})
			if e != nil {
				notices = append(notices, "Не удалось создать чат для восстановленного черновика: "+e.Error())
			} else {
				c.rememberLocked(doc)
				id = doc.ID
				adopted++
			}
		}
		if draft.ReplyToID != "" {
			doc, err := c.deps.Store.Load(id)
			if err != nil {
				// A failed read is not evidence that the referenced answer disappeared.
				notices = append(notices, "Не удалось проверить ссылку восстановленного черновика: "+err.Error())
			} else if !chat.ValidReference(doc.Messages, draft.ReplyToID, draft.ReplyToKind) {
				draft.ReplyToID, draft.ReplyToKind = "", ""
				draft.Revision++
				changed = true
				notices = append(notices, "Ссылка на недоступный ответ убрана из восстановленного черновика")
			}
		}
		c.drafts[id] = draft
		c.queues[id] = queue
	}
	if e := c.deps.Work.Prune(keep); e != nil {
		c.appendLog(fmt.Sprintf("Очистка рабочих файлов: %v\n", e))
	}
	if missing > 0 {
		notices = append(notices, fmt.Sprintf("Снимков очереди не найдено на диске: %d; они убраны из очереди", missing))
	}
	if adopted > 0 {
		notices = append(notices, "Незавершённая работа чата, которого нет в истории, перенесена в новый чат")
	}
	if changed {
		c.statePersist.mark()
	}
	blocks, e := c.deps.Work.LoadStream()
	if e != nil {
		notices = append(notices, "Сохранённые части ответа не прочитаны: "+e.Error())
	}
	for _, b := range blocks {
		if ok, err := c.deps.Store.Exists(b.ChatID); ok && err == nil {
			c.recovered = append(c.recovered, b)
		}
	}
	if len(c.recovered) != len(blocks) {
		c.streamPersist.mark()
	}
	return notices
}

func hasMessage(doc chat.Chat, id string) bool {
	for _, m := range doc.Messages {
		if m.ID == id {
			return true
		}
	}
	return false
}

// removeQueueFiles deletes PNGs whose queue entries were already removed in memory.
// The work state is written first, so a restart cannot bring a removed image back.
func (c *Controller) removeQueueFiles(paths []string) {
	if len(paths) == 0 {
		return
	}
	c.saveWorkNow()
	for _, p := range paths {
		_ = os.Remove(p)
	}
}

func joinNotices(notices []string) string { return strings.Join(notices, ". ") }
