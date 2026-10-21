package appserver

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/llm/codex/appserver/appservertest"
)

func TestMain(m *testing.M) {
	if os.Getenv(appservertest.EnvFake) != "" {
		appservertest.Main()
	}
	os.Exit(m.Run())
}

func start(t *testing.T) *Conn { return startWith(t, Options{Wait: 2 * time.Second}) }

func startWith(t *testing.T, o Options) *Conn {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	o.Binary, o.Dir = exe, t.TempDir()
	o.Env = append(os.Environ(), appservertest.EnvFake+"=1", appservertest.EnvStore+"="+t.TempDir())
	c, err := Start(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func thread(t *testing.T, c *Conn) string {
	t.Helper()
	var r struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := c.Call(context.Background(), "thread/start", map[string]any{"ephemeral": true}, &r); err != nil {
		t.Fatal(err)
	}
	return r.Thread.ID
}

func turn(text string) map[string]any {
	return map[string]any{"input": []map[string]any{{"type": "text", "text": text}}}
}

type previews struct {
	mu   sync.Mutex
	list []string
}

func (p *previews) add(s string) { p.mu.Lock(); p.list = append(p.list, s); p.mu.Unlock() }
func (p *previews) last() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.list) == 0 {
		return ""
	}
	return p.list[len(p.list)-1]
}

func TestTurnStreamsPreviewAndReturnsFinalText(t *testing.T) {
	c := start(t)
	var seen previews
	r, err := c.RunTurn(context.Background(), thread(t, c), turn("вопрос"), seen.add)
	if err != nil || r.Status != StatusCompleted || r.Text != "Ответ: вопрос" {
		t.Fatalf("result %+v err %v", r, err)
	}
	if len(seen.list) == 0 || !strings.HasPrefix("Ответ: вопрос", seen.list[0]) || seen.last() != "Ответ: вопрос" {
		t.Fatalf("previews %q", seen.list)
	}
	if r.Timing.FirstText <= 0 || r.Timing.Completed < r.Timing.FirstText {
		t.Fatalf("timing %+v", r.Timing)
	}
}

func TestInterleavedThreadsAreRoutedIndependently(t *testing.T) {
	c := start(t)
	a, b := thread(t, c), thread(t, c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	slow := make(chan error, 1)
	var slowSeen previews
	go func() {
		_, err := c.RunTurn(ctx, a, turn("[slow] A"), slowSeen.add)
		slow <- err
	}()
	var fastSeen previews
	r, err := c.RunTurn(context.Background(), b, turn("B"), fastSeen.add)
	if err != nil || r.Text != "Ответ: B" {
		t.Fatalf("B: %+v %v", r, err)
	}
	for _, p := range fastSeen.list {
		if strings.Contains(p, "A") {
			t.Fatalf("B received A text: %q", fastSeen.list)
		}
	}
	// Cancelling A interrupts only A; the connection keeps serving B.
	cancel()
	if err := <-slow; !errors.Is(err, context.Canceled) {
		t.Fatalf("A: %v", err)
	}
	if strings.Contains(slowSeen.last(), "B") {
		t.Fatalf("A received B text: %q", slowSeen.last())
	}
	if r, err = c.RunTurn(context.Background(), b, turn("снова"), nil); err != nil || r.Text != "Ответ: снова" {
		t.Fatalf("after interrupt: %+v %v", r, err)
	}
}

func TestPartialLinesEarlyNotificationsAndLateTurns(t *testing.T) {
	c := start(t)
	th := thread(t, c)
	for _, tc := range []struct{ marker, want string }{
		{"[split]", "Ответ: [split]"},
		{"[early]", "Ранний Ответ: [early]"},
		{"[late]", "Ответ: [late]"},
		{"[commentary]", "Ответ: [commentary]"},
	} {
		var seen previews
		r, err := c.RunTurn(context.Background(), th, turn(tc.marker), seen.add)
		if err != nil || r.Text != tc.want {
			t.Fatalf("%s: %+v %v", tc.marker, r, err)
		}
		for _, p := range seen.list {
			if strings.Contains(p, "СТАРЫЙ") || strings.Contains(p, "Думаю") {
				t.Fatalf("%s leaked %q", tc.marker, p)
			}
		}
	}
}

func TestFailedTurnAndTextLimit(t *testing.T) {
	c := start(t)
	th := thread(t, c)
	r, err := c.RunTurn(context.Background(), th, turn("[fail]"), nil)
	if err != nil || r.Status != StatusFailed || r.Error != "модель недоступна" {
		t.Fatalf("failed turn: %+v %v", r, err)
	}
	if _, err = c.RunTurn(context.Background(), th, turn("[big]"), nil); !errors.Is(err, ErrTextLimit) {
		t.Fatalf("limit: %v", err)
	}
}

func TestProcessCrashGivesUnknownOutcome(t *testing.T) {
	for _, marker := range []string{"[crash-before]", "[crash-after]", "[garbage]"} {
		c := start(t)
		var seen previews
		_, err := c.RunTurn(context.Background(), thread(t, c), turn(marker), seen.add)
		if !errors.Is(err, llm.ErrOutcomeUnknown) {
			t.Fatalf("%s: %v", marker, err)
		}
		if marker == "[crash-before]" && (!strings.Contains(err.Error(), "Error: failed to initialize state runtime") || strings.Contains(err.Error(), "secret-value")) {
			t.Fatalf("crash reason: %v", err)
		}
		if marker == "[crash-after]" && seen.last() == "" {
			t.Fatalf("%s: preview before crash was not delivered", marker)
		}
		select {
		case <-c.Done():
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: connection still open", marker)
		}
		if err := c.Call(context.Background(), "skills/list", map[string]any{}, nil); err == nil {
			t.Fatalf("%s: call on dead connection succeeded", marker)
		}
	}
}

func TestUnconfirmedInterruptIsUnknown(t *testing.T) {
	old := interruptTimeout
	interruptTimeout = 300 * time.Millisecond
	defer func() { interruptTimeout = old }()
	c := start(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	_, err := c.RunTurn(ctx, thread(t, c), turn("[ignore-interrupt]"), nil)
	if !errors.Is(err, llm.ErrOutcomeUnknown) {
		t.Fatalf("got %v", err)
	}
	// A possibly running turn must not survive into the next request.
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("connection kept a turn that did not confirm the interrupt")
	}
}

func TestCancelledBeforeStartSendsNothingAndKeepsConnection(t *testing.T) {
	c := start(t)
	th := thread(t, c)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.RunTurn(ctx, th, turn("q"), nil); !errors.Is(err, context.Canceled) || errors.Is(err, llm.ErrOutcomeUnknown) {
		t.Fatalf("got %v", err)
	}
	if r, err := c.RunTurn(context.Background(), th, turn("q"), nil); err != nil || r.Text != "Ответ: q" {
		t.Fatalf("after: %+v %v", r, err)
	}
}

func TestRPCErrorAndCloseStopsProcess(t *testing.T) {
	c := start(t)
	err := c.Call(context.Background(), "thread/resume", map[string]any{"threadId": "01a10000-0000-7000-8000-000000000000"}, nil)
	var rpc *RPCError
	if !errors.As(err, &rpc) || rpc.Code != -32600 {
		t.Fatalf("resume missing: %v", err)
	}
	if err = c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.exited:
	default:
		t.Fatal("process still running after Close")
	}
	if _, err = c.RunTurn(context.Background(), "x", turn("q"), nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed: %v", err)
	}
}

func TestStuckProcessDoesNotBlockCallsOrClose(t *testing.T) {
	c := startWith(t, Options{CallTimeout: 500 * time.Millisecond, Wait: 500 * time.Millisecond})
	th := thread(t, c)
	// The fake stops reading stdin while handling this turn/start; it is never answered.
	started := time.Now()
	if _, err := c.RunTurn(context.Background(), th, turn("[stop-reading]"), nil); !errors.Is(err, llm.ErrOutcomeUnknown) {
		t.Fatalf("unanswered turn/start: %v", err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("turn/start not bounded: %s", time.Since(started))
	}

	c = startWith(t, Options{CallTimeout: 30 * time.Second, Wait: 500 * time.Millisecond})
	th = thread(t, c)
	go func() { _, _ = c.RunTurn(context.Background(), th, turn("[stop-reading]"), nil) }()
	time.Sleep(200 * time.Millisecond)
	// A large request fills the pipe of a process that no longer reads it.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started = time.Now()
	err := c.Call(ctx, "skills/list", map[string]any{"padding": strings.Repeat("x", 8<<20)}, nil)
	if err == nil || time.Since(started) > 5*time.Second {
		t.Fatalf("blocked write: %v after %s", err, time.Since(started))
	}
	started = time.Now()
	_ = c.Close(context.Background())
	if time.Since(started) > 5*time.Second {
		t.Fatalf("Close blocked for %s", time.Since(started))
	}
	select {
	case <-c.exited:
	default:
		t.Fatal("process still running")
	}
}
