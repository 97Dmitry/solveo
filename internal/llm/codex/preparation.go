package codex

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/llm/codex/appserver"
)

var _ llm.ChatPreparer = (*Client)(nil)

type preparationKey struct{ chatID, instructions, model, reasoning string }

// All fields except the immutable key/context/channels are protected by app.mu.
// The gate bounds the entire lifetime, including in-flight start and cleanup.
type preparedThread struct {
	key            preparationKey
	ctx            context.Context
	cancel         context.CancelFunc
	conn           *appserver.Conn
	id             string
	ready, claimed chan struct{}
	finished       chan struct{}
	consumed       bool
}

// PrepareChat starts no turn. Codex itself may send a generate=false websocket
// warmup; no question is sent and no answer is generated. Cancellation does not
// abandon an in-flight thread/start: collect its ID and release it first.
func (c *Client) PrepareChat(parent context.Context, in llm.ChatPreparation) error {
	if in.ChatID == "" {
		return errors.New("не задан чат для подготовки")
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	select {
	case c.app.preparationGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.app.preparationGate }()
	if err := ctx.Err(); err != nil {
		return err
	}
	instructions := c.instructions(llm.Request{Config: in.Config, GlobalInstructions: in.GlobalInstructions})
	p := &preparedThread{key: preparationKey{chatID: in.ChatID, instructions: fmt.Sprintf("%x", sha256.Sum256([]byte(instructions))), model: in.Config.Model, reasoning: in.Config.Reasoning}, ctx: ctx, cancel: cancel, ready: make(chan struct{}), claimed: make(chan struct{}), finished: make(chan struct{})}
	c.app.mu.Lock()
	if c.app.closed {
		c.app.mu.Unlock()
		return errors.New("приложение завершается")
	}
	if c.app.lastSubmittedChat == in.ChatID || c.app.foreground > 0 {
		c.app.mu.Unlock()
		return nil
	}
	c.app.prepared = p
	c.app.mu.Unlock()
	ready := false
	defer func() {
		c.app.mu.Lock()
		if !ready {
			close(p.ready)
		}
		consumed := p.consumed
		c.app.mu.Unlock()
		if p.id != "" && !consumed {
			releaseCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			err := p.conn.Call(releaseCtx, "thread/unsubscribe", map[string]any{"threadId": p.id}, nil)
			stop()
			if err != nil {
				p.conn.Kill()
			} // preserve the resource bound if release is unconfirmed
		}
		c.app.mu.Lock()
		if c.app.prepared == p {
			c.app.prepared = nil
		}
		close(p.finished)
		c.app.mu.Unlock()
	}()
	conn, err := c.app.connection(ctx)
	if err != nil {
		return err
	}
	c.app.mu.Lock()
	p.conn = conn
	c.app.mu.Unlock()
	var account struct {
		Account *struct {
			Type string `json:"type"`
		} `json:"account"`
	}
	if err = conn.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &account); err != nil {
		return err
	}
	if account.Account == nil {
		return errors.New("для подготовки чата требуется вход в Codex")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	params := threadParams(c.app.work, in.Config)
	params["ephemeral"] = false
	params["developerInstructions"] = instructions
	var result struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	startCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
	err = conn.Call(startCtx, "thread/start", params, &result)
	stop()
	if err != nil {
		// An unanswered start may have created an unaddressable loaded thread.
		// No preparation is reusable on this process after such a transport loss.
		var rpc *appserver.RPCError
		if !errors.As(err, &rpc) {
			conn.Kill()
		}
		return err
	}
	if !validThreadID(result.Thread.ID) {
		conn.Kill()
		return errors.New("Codex вернул некорректный ID заготовки")
	}
	c.app.mu.Lock()
	p.id = result.Thread.ID
	ready = true
	close(p.ready)
	c.app.mu.Unlock()
	select {
	case <-p.claimed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-conn.Done():
		return conn.Err()
	}
}

// takePrepared waits for a matching in-flight start, then claims it atomically.
// Cancellation/staleness/process loss can only fall back before turn/start.
func (c *Client) takePrepared(ctx context.Context, conn *appserver.Conn, key preparationKey) (string, error) {
	for {
		c.app.mu.Lock()
		p := c.app.prepared
		if p == nil {
			c.app.mu.Unlock()
			return "", nil
		}
		if p.key != key || p.ctx.Err() != nil || (p.conn != nil && p.conn != conn) {
			p.cancel()
			c.app.mu.Unlock()
			// Keep the connection free of foreground turns until cleanup can no longer retire it.
			select {
			case <-p.finished:
				return "", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		select {
		case <-p.ready:
			if p.id == "" {
				c.app.mu.Unlock()
				return "", nil
			}
			p.consumed = true
			c.app.prepared = nil
			close(p.claimed)
			id := p.id
			c.app.mu.Unlock()
			return id, nil
		default:
			c.app.mu.Unlock()
			select {
			case <-p.ready:
			case <-p.ctx.Done():
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}
}
