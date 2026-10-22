package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/llm/codex/appserver/appservertest"
)

func prepareRequest() llm.Request {
	r := request("question")
	r.ChatID = "local-chat"
	r.GlobalInstructions = "immutable"
	return r
}

func backgroundThread(t *testing.T, c *Client, r llm.Request) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- c.PrepareChat(ctx, llm.ChatPreparation{ChatID: r.ChatID, Config: r.Config, GlobalInstructions: r.GlobalInstructions})
	}()
	t.Cleanup(cancel)
	return cancel, done
}

func awaitPreparation(t *testing.T, c *Client) *preparedThread {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		c.app.mu.Lock()
		p := c.app.prepared
		c.app.mu.Unlock()
		if p != nil {
			select {
			case <-p.ready:
				return p
			default:
			}
		}
		select {
		case <-deadline:
			t.Fatal("preparation did not become ready")
			return nil
		case <-time.After(time.Millisecond):
		}
	}
}
func awaitPrepareEnd(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("preparation did not release")
	}
}

func TestPreparedThreadIsConsumedOnceWithPendingInstructions(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	r := prepareRequest()
	_, done := backgroundThread(t, c, r)
	p := awaitPreparation(t, c)
	if count(readLog(t, env.log), "turn/start") != 0 {
		t.Fatal("background model turn")
	}
	out, err := c.Generate(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	awaitPrepareEnd(t, done)
	if s := decodeState(t, out.State); s.ThreadID != p.id || s.InstructionsHash == "" || s.PendingInstructionsHash != "" {
		t.Fatalf("state %+v", s)
	}
	r.State = out.State
	if _, err = c.Generate(context.Background(), r, nil); err != nil {
		t.Fatal(err)
	}
	lines := readLog(t, env.log)
	if count(lines, "thread/start") != 1 || count(lines, "thread/unsubscribe") != 0 || count(lines, "turn/start") != 2 {
		t.Fatalf("unexpected calls %+v", lines)
	}
}

func TestPreparedThreadFailureDoesNotConfirmInitialInstructions(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	r := prepareRequest()
	r.Turn.Text = "[fail] question"
	_, done := backgroundThread(t, c, r)
	p := awaitPreparation(t, c)
	out, err := c.Generate(context.Background(), r, nil)
	if err == nil {
		t.Fatal("expected turn failure")
	}
	awaitPrepareEnd(t, done)
	s := decodeState(t, out.State)
	if s.ThreadID != p.id || s.InstructionsHash != "" || s.PendingInstructionsHash == "" {
		t.Fatalf("state %+v", s)
	}
	r.State = out.State
	r.Turn.Text = "explicit retry"
	if _, err = c.Generate(context.Background(), r, nil); err != nil {
		t.Fatal(err)
	}
	lines := readLog(t, env.log)
	if count(lines, "thread/start") != 1 || !strings.Contains(string(lines[len(lines)-1].Params), "Обновление инструкций Solveo") {
		t.Fatal("retry did not repair instructions on same thread")
	}
}

func TestStalePreparedThreadIsReleased(t *testing.T) {
	for _, change := range []string{"chat", "instructions", "model", "reasoning"} {
		t.Run(change, func(t *testing.T) {
			var env fakeEnv
			c := fakeClient(t, &env)
			r := prepareRequest()
			_, done := backgroundThread(t, c, r)
			p := awaitPreparation(t, c)
			switch change {
			case "chat":
				r.ChatID = "another"
			case "instructions":
				r.GlobalInstructions = "changed"
			case "model":
				r.Config.Model = "other"
			case "reasoning":
				r.Config.Reasoning = "high"
			}
			out, err := c.Generate(context.Background(), r, nil)
			if err != nil {
				t.Fatal(err)
			}
			awaitPrepareEnd(t, done)
			if decodeState(t, out.State).ThreadID == p.id {
				t.Fatal("stale thread consumed")
			}
			lines := readLog(t, env.log)
			if count(lines, "thread/start") != 2 || count(lines, "thread/unsubscribe") != 1 || count(lines, "turn/start") != 1 {
				t.Fatalf("unexpected calls %+v", lines)
			}
		})
	}
}

func TestCanceledUnusedPreparationIsReleasedAndNeverSent(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	r := prepareRequest()
	cancel, done := backgroundThread(t, c, r)
	awaitPreparation(t, c)
	cancel()
	awaitPrepareEnd(t, done)
	lines := readLog(t, env.log)
	if count(lines, "thread/unsubscribe") != 1 || count(lines, "turn/start") != 0 {
		t.Fatalf("unexpected calls %+v", lines)
	}
}

func TestPreparationProcessLossIsNotReusedOrResent(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	r := prepareRequest()
	_, done := backgroundThread(t, c, r)
	p := awaitPreparation(t, c)
	p.conn.Kill()
	awaitPrepareEnd(t, done)
	out, err := c.Generate(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decodeState(t, out.State).ThreadID == p.id {
		t.Fatal("lost preparation reused")
	}
	lines := readLog(t, env.log)
	if count(lines, "start") != 2 || count(lines, "turn/start") != 1 {
		t.Fatalf("unexpected calls %+v", lines)
	}
}

func TestConcurrentThreadPreparationAndSubmission(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	r := prepareRequest()
	gate := filepath.Join(t.TempDir(), "start")
	t.Setenv(appservertest.EnvThreadGate, gate)
	_, done := backgroundThread(t, c, r)
	deadline := time.After(5 * time.Second)
	for {
		if _, err := os.Stat(gate + ".entered"); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no start")
		case <-time.After(time.Millisecond):
		}
	}
	result := make(chan error, 1)
	go func() { _, err := c.Generate(context.Background(), r, nil); result <- err }()
	if err := os.WriteFile(gate+".release", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	awaitPrepareEnd(t, done)
	lines := readLog(t, env.log)
	if count(lines, "thread/start") != 1 || count(lines, "turn/start") != 1 {
		t.Fatalf("duplicate preparation/send %+v", lines)
	}
}

func TestPreparationFailureFallsBackBeforeAnyTurn(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	r := prepareRequest()
	t.Setenv(appservertest.EnvThreadError, "1")
	_, done := backgroundThread(t, c, r)
	awaitPrepareEnd(t, done)
	out, err := c.Generate(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.State) == 0 {
		t.Fatal("question lost")
	}
	if count(readLog(t, env.log), "turn/start") != 1 {
		t.Fatal("question sent twice")
	}
}

func TestCloseReleasesPreparedThreadAndStopsWorker(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	r := prepareRequest()
	_, done := backgroundThread(t, c, r)
	awaitPreparation(t, c)
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	awaitPrepareEnd(t, done)
	if count(readLog(t, env.log), "turn/start") != 0 {
		t.Fatal("close sent a model turn")
	}
}

func TestUnknownPreparedTurnIsNeverAutomaticallyResent(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	r := prepareRequest()
	r.Turn.Text = "[crash-after] question"
	_, done := backgroundThread(t, c, r)
	awaitPreparation(t, c)
	out, err := c.Generate(context.Background(), r, nil)
	awaitPrepareEnd(t, done)
	if !errors.Is(err, llm.ErrOutcomeUnknown) || len(out.State) == 0 {
		t.Fatalf("%+v %v", out, err)
	}
	if count(readLog(t, env.log), "turn/start") != 1 {
		t.Fatal("unknown turn resent")
	}
}

func TestPreparationGateBoundsUnusedThreads(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	r := prepareRequest()
	cancel, done := backgroundThread(t, c, r)
	first := awaitPreparation(t, c)
	r.ChatID = "next-chat"
	cancelNext, nextDone := backgroundThread(t, c, r)
	// The second worker can only start after the first has released its thread.
	cancel()
	awaitPrepareEnd(t, done)
	next := awaitPreparation(t, c)
	if next.id == first.id {
		t.Fatal("reused canceled preparation")
	}
	lines := readLog(t, env.log)
	if count(lines, "thread/start") != 2 || count(lines, "thread/unsubscribe") != 1 || count(lines, "turn/start") != 0 {
		t.Fatalf("unbounded resources %+v", lines)
	}
	cancelNext()
	awaitPrepareEnd(t, nextDone)
}

func TestLatePreparationAfterSubmissionDoesNotCreateThread(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	r := prepareRequest()
	if _, err := c.Generate(context.Background(), r, nil); err != nil {
		t.Fatal(err)
	}
	_, done := backgroundThread(t, c, r)
	awaitPrepareEnd(t, done)
	lines := readLog(t, env.log)
	if count(lines, "thread/start") != 1 || count(lines, "turn/start") != 1 {
		t.Fatal("late worker created an unused thread")
	}
}

func TestCanceledInFlightStartIsCollectedAndReleased(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	r := prepareRequest()
	gate := filepath.Join(t.TempDir(), "start")
	t.Setenv(appservertest.EnvThreadGate, gate)
	cancel, done := backgroundThread(t, c, r)
	deadline := time.After(5 * time.Second)
	for {
		if _, err := os.Stat(gate + ".entered"); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no start")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := os.WriteFile(gate+".release", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	awaitPrepareEnd(t, done)
	lines := readLog(t, env.log)
	if count(lines, "thread/start") != 1 || count(lines, "thread/unsubscribe") != 1 || count(lines, "turn/start") != 0 {
		t.Fatal("cancel abandoned an in-flight thread")
	}
}

func TestPreparationReleaseFailureRetiresProcess(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	r := prepareRequest()
	t.Setenv(appservertest.EnvUnsubscribeError, "1")
	cancel, done := backgroundThread(t, c, r)
	p := awaitPreparation(t, c)
	cancel()
	awaitPrepareEnd(t, done)
	select {
	case <-p.conn.Done():
	default:
		t.Fatal("failed release left a reusable process with leaked threads")
	}
}

func TestStaleCleanupFinishesBeforeForegroundUse(t *testing.T) {
	for _, mode := range []string{"new", "resume", "check"} {
		t.Run(mode, func(t *testing.T) {
			var env fakeEnv
			c := fakeClient(t, &env)
			gate := filepath.Join(t.TempDir(), "cleanup")
			t.Setenv(appservertest.EnvUnsubscribeGate, gate)
			t.Setenv(appservertest.EnvUnsubscribeError, "1")
			seed := prepareRequest()
			seed.ChatID = "existing"
			out, err := c.Generate(context.Background(), seed, nil)
			if err != nil {
				t.Fatal(err)
			}
			prep := prepareRequest()
			cancel, done := backgroundThread(t, c, prep)
			awaitPreparation(t, c)
			cancel()
			deadline := time.After(5 * time.Second)
			for {
				if _, err = os.Stat(gate + ".entered"); err == nil {
					break
				}
				select {
				case <-deadline:
					t.Fatal("no cleanup")
				case <-time.After(time.Millisecond):
				}
			}
			result := make(chan error, 1)
			go func() {
				r := prepareRequest()
				r.ChatID = "another"
				if mode == "resume" {
					r = seed
					r.State = out.State
				}
				var e error
				if mode == "check" {
					_, e = c.Check(context.Background(), r.Config, nil)
				} else {
					_, e = c.Generate(context.Background(), r, nil)
				}
				result <- e
			}()
			early := false
			select {
			case <-result:
				early = true
			case <-time.After(100 * time.Millisecond):
			}
			before := count(readLog(t, env.log), "turn/start")
			if err = os.WriteFile(gate+".release", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			awaitPrepareEnd(t, done)
			if !early {
				select {
				case err = <-result:
				case <-time.After(5 * time.Second):
					t.Fatal("foreground did not recover")
				}
			}
			if early || before != 1 {
				t.Fatal("foreground used a connection while stale cleanup could retire it")
			}
			if err != nil {
				t.Fatalf("foreground did not recover after retirement: %v", err)
			}
		})
	}
}

func TestClaimedPreparationStateSurvivesEarlyAccountFailure(t *testing.T) {
	for _, mode := range []string{"error", "none", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			var env fakeEnv
			c := fakeClient(t, &env)
			r := prepareRequest()
			gate := filepath.Join(t.TempDir(), "account")
			setting := mode
			if mode == "cancel" {
				setting = "gate:" + gate
			}
			t.Setenv(appservertest.EnvAccountAfterStart, setting)
			_, done := backgroundThread(t, c, r)
			p := awaitPreparation(t, c)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type response struct {
				out llm.Result
				err error
			}
			result := make(chan response, 1)
			go func() { out, err := c.Generate(ctx, r, nil); result <- response{out, err} }()
			if mode == "cancel" {
				deadline := time.After(5 * time.Second)
				for {
					if _, err := os.Stat(gate + ".entered"); err == nil {
						break
					}
					select {
					case <-deadline:
						t.Fatal("no account read")
					case <-time.After(time.Millisecond):
					}
				}
				cancel()
			}
			got := <-result
			if mode == "cancel" {
				if err := os.WriteFile(gate+".release", nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			awaitPrepareEnd(t, done)
			if got.err == nil {
				t.Fatal("expected early account failure")
			}
			if len(got.out.State) == 0 {
				t.Fatal("claimed thread lost on early return")
			}
			state := decodeState(t, got.out.State)
			if state.ThreadID != p.id || state.InstructionsHash != "" || state.PendingInstructionsHash == "" {
				t.Fatalf("incorrect initial state: %+v", state)
			}
			if count(readLog(t, env.log), "turn/start") != 0 {
				t.Fatal("question sent despite account failure")
			}
		})
	}
}

// Preparation must not add a second request path to ordinary follow-ups.
func TestPreparedAndImmediateFollowupsUseSameRPCsAndInput(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	var payloads []map[string]any
	for _, prepared := range []bool{false, true} {
		r := prepareRequest()
		r.ChatID = chat.ID()
		var done <-chan error
		if prepared {
			_, done = backgroundThread(t, c, r)
			awaitPreparation(t, c)
		}
		first, err := c.Generate(context.Background(), r, nil)
		if err != nil {
			t.Fatal(err)
		}
		if prepared {
			awaitPrepareEnd(t, done)
		}
		r.State = first.State
		r.Turn.Text = "same follow-up"
		before := len(readLog(t, env.log))
		out, err := c.Generate(context.Background(), r, nil)
		if err != nil {
			t.Fatal(err)
		}
		if decodeState(t, out.State).ThreadID != decodeState(t, first.State).ThreadID {
			t.Fatal("follow-up replaced thread")
		}
		lines := readLog(t, env.log)[before:]
		if len(lines) != 2 || lines[0].Method != "account/read" || lines[1].Method != "turn/start" {
			t.Fatalf("extra follow-up RPCs: %+v", lines)
		}
		var p map[string]any
		if err = json.Unmarshal(lines[1].Params, &p); err != nil {
			t.Fatal(err)
		}
		delete(p, "threadId")
		payloads = append(payloads, p)
	}
	if !reflect.DeepEqual(payloads[0], payloads[1]) {
		t.Fatalf("different follow-up inputs: %+v / %+v", payloads[0], payloads[1])
	}
}
