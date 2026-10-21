package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/97Dmitry/solveo/internal/llm"
)

// Turn statuses reported by turn/completed.
const (
	StatusCompleted   = "completed"
	StatusInterrupted = "interrupted"
	StatusFailed      = "failed"
)

const phaseCommentary = "commentary"

var ErrTextLimit = errors.New("ответ Codex длиннее 1 МиБ")

// Timing records monotonic offsets of one turn from its turn/start request.
type Timing struct {
	Sent          time.Time
	FirstText     time.Duration
	FirstReadable time.Duration
	Completed     time.Duration
}

// TurnResult is the confirmed outcome of a turn. Text is the final answer item.
type TurnResult struct {
	ID     string
	Status string
	Text   string
	Error  string
	Timing Timing
}

type item struct {
	phase string
	text  strings.Builder
	done  bool
}

// turnState accumulates notifications of one turn. Only one turn of a thread is
// awaited at a time; notifications of any other turn ID are ignored.
type turnState struct {
	items    map[string]*item
	order    []string
	current  string
	changed  bool
	overflow bool
	done     bool
	status   string
	errText  string
	lastErr  string
	first    time.Time
	readable time.Time
	ended    time.Time
}

type subscription struct {
	mu     sync.Mutex
	turns  map[string]*turnState
	signal chan struct{}
}

func newSubscription() *subscription {
	return &subscription{turns: map[string]*turnState{}, signal: make(chan struct{}, 1)}
}

func (s *subscription) wake() {
	select {
	case s.signal <- struct{}{}:
	default:
	}
}

func (s *subscription) state(turnID string) *turnState {
	t := s.turns[turnID]
	if t == nil {
		// Bound buffered states for turns we never await (late events of an old turn).
		if len(s.turns) >= 4 {
			return nil
		}
		t = &turnState{items: map[string]*item{}}
		s.turns[turnID] = t
	}
	return t
}

func (s *subscription) handle(method string, params json.RawMessage) {
	var p struct {
		TurnID string `json:"turnId"`
		ItemID string `json:"itemId"`
		Delta  string `json:"delta"`
		Item   struct {
			Type  string  `json:"type"`
			ID    string  `json:"id"`
			Phase *string `json:"phase"`
			Text  string  `json:"text"`
		} `json:"item"`
		Turn struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		WillRetry bool `json:"willRetry"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	turnID := p.TurnID
	if turnID == "" {
		turnID = p.Turn.ID
	}
	if turnID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.state(turnID)
	if t == nil || t.done {
		return
	}
	now := time.Now()
	switch method {
	case "item/started", "item/completed":
		if p.Item.Type != "agentMessage" || p.Item.ID == "" {
			return
		}
		it := t.item(p.Item.ID)
		if p.Item.Phase != nil {
			it.phase = *p.Item.Phase
		}
		if method == "item/completed" {
			// The completed item is authoritative; deltas are only a preview.
			if len(p.Item.Text) > MaxText {
				t.overflow = true
			} else {
				it.text.Reset()
				it.text.WriteString(p.Item.Text)
			}
			it.done = true
		}
		if it.phase != phaseCommentary {
			t.current = p.Item.ID
			t.changed = true
		}
	case "item/agentMessage/delta":
		if p.ItemID == "" {
			return
		}
		it := t.item(p.ItemID)
		if it.phase == phaseCommentary || it.done {
			return
		}
		if it.text.Len()+len(p.Delta) > MaxText {
			t.overflow = true
			s.wake()
			return
		}
		it.text.WriteString(p.Delta)
		t.current = p.ItemID
		t.changed = true
		if t.first.IsZero() {
			t.first = now
		}
		if t.readable.IsZero() && (it.text.Len() >= 80 || strings.Contains(p.Delta, "\n")) {
			t.readable = now
		}
	case "error":
		if !p.WillRetry {
			t.lastErr = p.Error.Message
		}
	case "turn/completed":
		t.done = true
		t.status = p.Turn.Status
		if p.Turn.Error != nil {
			t.errText = p.Turn.Error.Message
		}
		t.ended = now
	default:
		return
	}
	s.wake()
}

func (t *turnState) item(id string) *item {
	it := t.items[id]
	if it == nil {
		it = &item{}
		t.items[id] = it
		t.order = append(t.order, id)
	}
	return it
}

// final returns the last completed final_answer item, otherwise the last
// completed non-commentary item. Commentary is never an answer.
func (t *turnState) final() string {
	fallback := ""
	found := false
	for i := len(t.order) - 1; i >= 0; i-- {
		it := t.items[t.order[i]]
		if !it.done || it.phase == phaseCommentary {
			continue
		}
		if it.phase == "final_answer" {
			return it.text.String()
		}
		if !found {
			fallback, found = it.text.String(), true
		}
	}
	return fallback
}

// RunTurn starts a turn in a thread and waits for its confirmed completion.
// onText receives the full preview text of the current answer item whenever it
// changes. Cancelling ctx sends turn/interrupt; the result is ctx.Err() only
// when the interrupt is confirmed. turn/start on a thread with an active turn
// adds input to that turn, so a turn must never be left running unseen: if the
// server does not answer turn/start or confirm an interrupt, the connection is
// killed and every affected request gets llm.ErrOutcomeUnknown.
func (c *Conn) RunTurn(ctx context.Context, threadID string, params map[string]any, onText func(string)) (TurnResult, error) {
	s := newSubscription()
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return TurnResult{}, err
	}
	if c.threads[threadID] != nil {
		c.mu.Unlock()
		return TurnResult{}, errors.New("в этом thread уже выполняется ответ")
	}
	c.threads[threadID] = s
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.threads, threadID)
		c.mu.Unlock()
	}()

	params["threadId"] = threadID
	result := TurnResult{Timing: Timing{Sent: time.Now()}}
	if err := ctx.Err(); err != nil {
		return result, err // nothing was sent
	}
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	// Always wait for the turn ID, even if ctx is cancelled meanwhile: without it
	// the turn could neither be interrupted nor told apart from the next one.
	startCtx, cancelStart := context.WithTimeout(context.Background(), c.opts.CallTimeout)
	err := c.Call(startCtx, "turn/start", params, &started)
	cancelStart()
	if err != nil {
		if _, ok := errors.AsType[*RPCError](err); ok {
			// The server rejected the request: no turn exists.
			return result, err
		}
		// The request may have reached the server and started a turn.
		c.Kill()
		return result, fmt.Errorf("%w: %w", llm.ErrOutcomeUnknown, err)
	}
	if started.Turn.ID == "" {
		return result, fmt.Errorf("%w: Codex app-server не вернул ID ответа", llm.ErrOutcomeUnknown)
	}
	result.ID = started.Turn.ID

	interrupted := false
	var interruptDeadline <-chan time.Time
	stop := ctx.Done()
	for {
		s.mu.Lock()
		t := s.state(result.ID)
		var preview string
		changed := false
		if t != nil && t.changed && t.current != "" {
			preview, changed = t.items[t.current].text.String(), true
			t.changed = false
		}
		overflow := t != nil && t.overflow
		var done bool
		if t != nil && t.done {
			done = true
			result.Status = t.status
			result.Text = t.final()
			result.Error = t.errText
			if result.Error == "" {
				result.Error = t.lastErr
			}
			result.Timing.FirstText = since(result.Timing.Sent, t.first)
			result.Timing.FirstReadable = since(result.Timing.Sent, t.readable)
			result.Timing.Completed = since(result.Timing.Sent, t.ended)
		}
		s.mu.Unlock()
		if changed && onText != nil && !interrupted {
			onText(preview)
		}
		if done {
			switch {
			case overflow:
				return result, ErrTextLimit
			case ctx.Err() != nil:
				// Cancellation wins even if the turn finished while the interrupt was in flight.
				return result, ctx.Err()
			}
			return result, nil
		}
		if overflow && !interrupted {
			// Stop the turn instead of retaining an unbounded answer.
			interrupted = true
			stop = nil
			interruptDeadline = c.interrupt(threadID, result.ID)
			continue
		}
		select {
		case <-s.signal:
		case <-c.done:
			// Re-check once: completion may have arrived just before the connection closed.
			s.mu.Lock()
			t = s.state(result.ID)
			finished := t != nil && t.done
			s.mu.Unlock()
			if finished {
				continue
			}
			return result, fmt.Errorf("%w: %w", llm.ErrOutcomeUnknown, c.Err())
		case <-stop:
			interrupted = true
			stop = nil
			interruptDeadline = c.interrupt(threadID, result.ID)
		case <-interruptDeadline:
			// The turn may still be running; restart instead of steering into it later.
			c.Kill()
			return result, fmt.Errorf("%w: Codex не подтвердил отмену ответа, подключение перезапущено", llm.ErrOutcomeUnknown)
		}
	}
}

// interrupt asks the server to stop a turn without waiting for the reply on the
// caller's (already cancelled) context. The returned channel fires when the
// confirmation deadline passes.
func (c *Conn) interrupt(threadID, turnID string) <-chan time.Time {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), interruptTimeout)
		defer cancel()
		_ = c.Call(ctx, "turn/interrupt", map[string]any{"threadId": threadID, "turnId": turnID}, nil)
	}()
	return time.After(interruptTimeout)
}

func since(from, to time.Time) time.Duration {
	if to.IsZero() {
		return 0
	}
	return to.Sub(from)
}
