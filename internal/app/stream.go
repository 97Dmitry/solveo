package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/workstate"
)

// Stream blocks of one user turn.
const (
	blockMessage  = "message"
	blockApproach = chat.AnswerApproach
	blockSolution = chat.AnswerSolution
)

// streamFlushInterval coalesces provider text updates so the renderer is not
// redrawn per token. Each update carries the full preview, so dropping
// intermediate ones is safe.
const streamFlushInterval = 100 * time.Millisecond

// StreamBlock is unfinished answer text of a running request. It is never
// persisted and never becomes a completed answer by itself.
type StreamBlock struct {
	ChatID      string `json:"chat_id"`
	OperationID string `json:"operation_id"`
	MessageID   string `json:"message_id"`
	AttemptID   string `json:"attempt_id"`
	Block       string `json:"block"`
	Text        string `json:"text"`
}

// StreamSnapshot holds every live preview; Revision orders snapshots.
type StreamSnapshot struct {
	Revision uint64        `json:"revision"`
	Blocks   []StreamBlock `json:"blocks"`
}

type streamKey struct{ opID, attemptID, block string }

// streamSink returns the event handler of one Generate call. Previews are
// accepted only while that operation is current and the block is still open.
func (c *Controller) streamSink(opID, chatID, messageID, attemptID, block string) func(llm.Event) {
	key := streamKey{opID, attemptID, block}
	c.mu.Lock()
	c.streamOpen[key] = true
	c.mu.Unlock()
	return func(v llm.Event) {
		switch v.Kind {
		case llm.EventAnswerText:
			c.mu.Lock()
			if c.op.ID != opID || !c.streamOpen[key] {
				c.mu.Unlock()
				return
			}
			c.streams[key] = StreamBlock{ChatID: chatID, OperationID: opID, MessageID: messageID, AttemptID: attemptID, Block: block, Text: v.Text}
			// The checkpoint outlives closeStream until the outcome is archived.
			c.checkpoints[key] = workstate.Block{ChatID: chatID, MessageID: messageID, AttemptID: attemptID, Block: block, Text: v.Text, UpdatedAt: c.deps.Now()}
			c.scheduleStreamLocked()
			c.mu.Unlock()
			c.streamPersist.mark()
		default:
			c.appendLog(v.Text)
		}
	}
}

// closeStream removes a block's preview and ignores its later updates.
func (c *Controller) closeStream(opID, attemptID, block string) {
	key := streamKey{opID, attemptID, block}
	c.mu.Lock()
	delete(c.streamOpen, key)
	if _, ok := c.streams[key]; ok {
		delete(c.streams, key)
		c.scheduleStreamLocked()
	}
	c.mu.Unlock()
}

func (c *Controller) scheduleStreamLocked() {
	c.streamRevision++
	if c.deps.Emit == nil || c.streamFlushPending {
		return
	}
	c.streamFlushPending = true
	time.AfterFunc(streamFlushInterval, c.flushStream)
}

func (c *Controller) flushStream() {
	c.mu.Lock()
	c.streamFlushPending = false
	snap := c.streamSnapshotLocked()
	c.mu.Unlock()
	c.deps.Emit("solveo:stream", snap)
}

func (c *Controller) streamSnapshotLocked() StreamSnapshot {
	blocks := make([]StreamBlock, 0, len(c.streams))
	for _, b := range c.streams {
		blocks = append(blocks, b)
	}
	return StreamSnapshot{Revision: c.streamRevision, Blocks: blocks}
}

// StreamSnapshot returns live previews, used after a renderer reload.
func (c *Controller) StreamSnapshot() StreamSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.streamSnapshotLocked()
}

// connection returns the provider's optional account/connection capability.
func (c *Controller) connection() (llm.Connection, bool) {
	conn, ok := c.deps.Provider.(llm.Connection)
	return conn, ok
}

// prepareConnection starts the provider connection in the background without a
// model turn. Failures are only logged; the next request reports them.
func (c *Controller) prepareConnection() {
	conn, ok := c.connection()
	if !ok {
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	ctx := c.ctx
	c.workers.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.workers.Done()
		if err := conn.Prepare(ctx); err != nil && ctx.Err() == nil {
			c.appendLog(fmt.Sprintf("Подключение: %v\n", err))
		}
	}()
}

const accountTimeout = 30 * time.Second

// Account reports sign-in state of the streaming connection; it does not touch chats.
func (c *Controller) Account() (llm.Account, error) {
	conn, ok := c.connection()
	if !ok {
		return llm.Account{}, errNoConnection
	}
	ctx, cancel := context.WithTimeout(c.ctx, accountTimeout)
	defer cancel()
	return conn.Account(ctx)
}

// Login states shown in connection settings.
const (
	loginPending = "pending"
	loginDone    = "done"
	loginFailed  = "failed"
)

// LoginView is the device-code sign-in shown to the user; Go owns its state.
type LoginView struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Code  string `json:"code"`
	State string `json:"state"`
	Error string `json:"error"`
}

func (c *Controller) StartLogin() (Snapshot, error) {
	conn, ok := c.connection()
	if !ok {
		return Snapshot{}, errNoConnection
	}
	ctx, cancel := context.WithTimeout(c.ctx, accountTimeout)
	defer cancel()
	apply := func(id string, e error) {
		c.mu.Lock()
		if c.login.ID == id {
			c.login.State, c.login.Error = loginDone, ""
			if e != nil {
				c.login.State, c.login.Error = loginFailed, e.Error()
			} else {
				c.prepareChatLocked()
			}
			c.changedLocked()
		}
		c.mu.Unlock()
		c.publish()
	}
	// done may run before or after StartLogin returns: keep an early result
	// until the pending LoginView exists, so a completion is never lost.
	var mu sync.Mutex
	assigned := ""
	var early *error
	prompt, err := conn.StartLogin(ctx, func(e error) {
		mu.Lock()
		id := assigned
		if id == "" {
			early = &e
		}
		mu.Unlock()
		if id != "" {
			apply(id, e)
		}
	})
	if err != nil {
		return Snapshot{}, err
	}
	c.mu.Lock()
	c.login = LoginView{ID: prompt.ID, URL: prompt.URL, Code: prompt.Code, State: loginPending}
	c.changedLocked()
	c.mu.Unlock()
	mu.Lock()
	assigned = prompt.ID
	result := early
	mu.Unlock()
	if result != nil {
		apply(prompt.ID, *result)
	} else {
		c.publish()
	}
	return c.Snapshot(), nil
}

func (c *Controller) CancelLogin() (Snapshot, error) {
	conn, ok := c.connection()
	if !ok {
		return Snapshot{}, errNoConnection
	}
	c.mu.Lock()
	id := c.login.ID
	c.login = LoginView{}
	c.changedLocked()
	c.mu.Unlock()
	var err error
	if id != "" {
		ctx, cancel := context.WithTimeout(c.ctx, accountTimeout)
		err = conn.CancelLogin(ctx, id)
		cancel()
	}
	c.publish()
	return c.Snapshot(), err
}
