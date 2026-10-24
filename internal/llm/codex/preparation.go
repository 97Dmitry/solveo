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
	instructions := c.instructions(llm.Request{Config: in.Config, GlobalInstructions: in.GlobalInstructions})
	key := preparationKey{chatID: in.ChatID, instructions: fmt.Sprintf("%x", sha256.Sum256([]byte(instructions))), model: in.Config.Model, reasoning: in.Config.Reasoning}
	params := threadParams(c.app.work, in.Config)
	params["ephemeral"] = false
	params["developerInstructions"] = instructions
	return c.app.prepare(parent, key, params)
}

// prepare owns one thread/start until a matching run claims it or ctx ends.
func (a *appServer) prepare(parent context.Context, key preparationKey, params map[string]any) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	select {
	case a.preparationGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-a.preparationGate }()
	if err := ctx.Err(); err != nil {
		return err
	}
	p := &preparedThread{key: key, ctx: ctx, cancel: cancel, ready: make(chan struct{}), claimed: make(chan struct{}), finished: make(chan struct{})}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return errors.New("приложение завершается")
	}
	if a.lastSubmittedChat == key.chatID || a.foreground > 0 {
		a.mu.Unlock()
		return nil
	}
	a.prepared = p
	a.mu.Unlock()
	ready := false
	defer func() {
		a.mu.Lock()
		if !ready {
			close(p.ready)
		}
		consumed := p.consumed
		a.mu.Unlock()
		if p.id != "" && !consumed {
			releaseCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			err := p.conn.Call(releaseCtx, "thread/unsubscribe", map[string]any{"threadId": p.id}, nil)
			stop()
			if err != nil {
				p.conn.Kill()
			} // preserve the resource bound if release is unconfirmed
		}
		a.mu.Lock()
		if a.prepared == p {
			a.prepared = nil
		}
		close(p.finished)
		a.mu.Unlock()
	}()
	conn, err := a.connection(ctx)
	if err != nil {
		return err
	}
	a.mu.Lock()
	p.conn = conn
	a.mu.Unlock()
	var account struct {
		Account *struct {
			Type string `json:"type"`
		} `json:"account"`
	}
	if err = conn.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &account); err != nil {
		return err
	}
	if account.Account == nil {
		return errors.New("для подготовки требуется вход в Codex")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
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
	a.mu.Lock()
	p.id = result.Thread.ID
	ready = true
	close(p.ready)
	a.mu.Unlock()
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
func (a *appServer) takePrepared(ctx context.Context, conn *appserver.Conn, key preparationKey) (string, error) {
	for {
		a.mu.Lock()
		p := a.prepared
		if p == nil {
			a.mu.Unlock()
			return "", nil
		}
		if p.key != key || p.ctx.Err() != nil || (p.conn != nil && p.conn != conn) {
			p.cancel()
			a.mu.Unlock()
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
				a.mu.Unlock()
				return "", nil
			}
			p.consumed = true
			a.prepared = nil
			close(p.claimed)
			id := p.id
			a.mu.Unlock()
			return id, nil
		default:
			a.mu.Unlock()
			select {
			case <-p.ready:
			case <-p.ctx.Done():
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}
}
