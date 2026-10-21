package codex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
)

func TestInstructionsStartChangeAndRestart(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	c.Prompt = "CUSTOM SOLVEO"
	r := llm.Request{GlobalInstructions: "GLOBAL", Config: chat.Config{Instructions: "LOCAL"}, Turn: chat.Message{Text: "question"}}
	first, err := c.Generate(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.State = first.State
	if _, err = c.Generate(context.Background(), r, nil); err != nil {
		t.Fatal(err)
	}
	lines := readLog(t, env.log)
	for _, l := range lines {
		if l.Method == "thread/start" && (!strings.Contains(string(l.Params), "GLOBAL") || !strings.Contains(string(l.Params), "CUSTOM SOLVEO")) {
			t.Fatal("start lacks instructions")
		}
		if l.Method == "turn/start" && strings.Contains(string(l.Params), "LOCAL") {
			t.Fatal("ordinary question duplicates configuration")
		}
	}
	r.Config.Instructions = "CHANGED"
	changed, err := c.Generate(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	c = fakeClient(t, &env)
	c.Prompt = "CUSTOM SOLVEO"
	r.State = changed.State
	r.Turn.Text = "[show-instructions]"
	resumed, err := c.Generate(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resumed.Text, "CHANGED") || !strings.Contains(resumed.Text, "GLOBAL") {
		t.Fatalf("lost persisted instructions: %s", resumed.Text)
	}
	updates := 0
	for _, l := range readLog(t, env.log) {
		if l.Method == "turn/start" && strings.Contains(string(l.Params), "CHANGED") {
			updates++
		}
		if l.Method == "thread/resume" && strings.Contains(string(l.Params), "developerInstructions") {
			t.Fatal("resume reapplied instructions")
		}
	}
	if updates != 1 {
		t.Fatalf("updates: %d", updates)
	}
}

func TestInstructionUpdateFailureAndUnknown(t *testing.T) {
	for _, marker := range []string{"[fail]", "[crash-after]"} {
		t.Run(marker, func(t *testing.T) {
			var env fakeEnv
			c := fakeClient(t, &env)
			r := llm.Request{Config: chat.Config{Instructions: "old"}, Turn: chat.Message{Text: "first"}}
			first, err := c.Generate(context.Background(), r, nil)
			if err != nil {
				t.Fatal(err)
			}
			r.State = first.State
			r.Config.Instructions = "new"
			r.Turn.Text = marker
			failed, err := c.Generate(context.Background(), r, nil)
			if err == nil {
				t.Fatal("expected failure")
			}
			if marker == "[crash-after]" && !errors.Is(err, llm.ErrOutcomeUnknown) {
				t.Fatal(err)
			}
			var before, after sessionState
			_ = json.Unmarshal(first.State, &before)
			_ = json.Unmarshal(failed.State, &after)
			if before.InstructionsHash == "" || before.InstructionsHash != after.InstructionsHash {
				t.Fatal("unconfirmed instruction marked applied")
			}
			if marker == "[crash-after]" && after.PendingInstructionsHash == "" {
				t.Fatal("unknown update lost")
			}
			if count(readLog(t, env.log), "turn/start") != 2 {
				t.Fatal("automatic resend")
			}
		})
	}
}

func TestEphemeralApproachGetsSameInstructionsAndCustomPrompt(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	c.Prompt = "custom base"
	r := llm.Request{GlobalInstructions: "common", Config: chat.Config{Instructions: "local"}, Purpose: chat.AnswerApproach, Turn: chat.Message{Text: "[show-instructions]"}}
	approach, err := c.Generate(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(approach.State) != 0 {
		t.Fatal("ephemeral state escaped")
	}
	r.Purpose = chat.AnswerSolution
	solution, err := c.Generate(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if approach.Text != solution.Text || !strings.Contains(solution.Text, "custom base") || !strings.Contains(solution.Text, "common") || !strings.Contains(solution.Text, "local") {
		t.Fatal("instructions differ")
	}
}

func TestUnknownUpdateSurvivesRestartAndExplicitRetry(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	r := llm.Request{Config: chat.Config{Instructions: "original"}, Turn: chat.Message{Text: "first"}}
	first, err := c.Generate(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.State = first.State
	r.Config.Instructions = "updated"
	r.Turn.Text = "[crash-after]"
	lost, err := c.Generate(context.Background(), r, nil)
	if !errors.Is(err, llm.ErrOutcomeUnknown) {
		t.Fatal(err)
	}
	_ = c.Close(context.Background())
	c = fakeClient(t, &env)
	r.State = lost.State
	r.Turn.Text = "explicit retry"
	retry, err := c.Generate(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	var state sessionState
	_ = json.Unmarshal(retry.State, &state)
	if state.InstructionsHash == "" || state.PendingInstructionsHash != "" {
		t.Fatal("retry not confirmed")
	}
	r.State = retry.State
	r.Turn.Text = "[show-instructions]"
	final, err := c.Generate(context.Background(), r, nil)
	if err != nil || !strings.Contains(final.Text, "updated") {
		t.Fatal(final, err)
	}
	if count(readLog(t, env.log), "turn/start") != 4 {
		t.Fatal("automatic resubmission")
	}
}

func TestOldProviderStateGetsOneExplicitUpdate(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	r := llm.Request{Turn: chat.Message{Text: "first"}}
	out, err := c.Generate(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	var state sessionState
	_ = json.Unmarshal(out.State, &state)
	state.InstructionsHash = ""
	r.State, _ = json.Marshal(state)
	out, err = c.Generate(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.State = out.State
	r.Config.Model = "other-model"
	if _, err = c.Generate(context.Background(), r, nil); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range readLog(t, env.log) {
		if l.Method == "turn/start" && strings.Contains(string(l.Params), "Обновление инструкций Solveo") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("legacy updates: %d", n)
	}
}

func TestApproachFormatHasPriorityOverUserPreferences(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	r := llm.Request{Purpose: chat.AnswerApproach, GlobalInstructions: "Всегда приводи полный код", Turn: chat.Message{Text: "task"}}
	if _, err := c.Generate(context.Background(), r, nil); err != nil {
		t.Fatal(err)
	}
	for _, l := range readLog(t, env.log) {
		if l.Method == "thread/start" {
			var p struct {
				Instructions string `json:"developerInstructions"`
			}
			_ = json.Unmarshal(l.Params, &p)
			if !strings.Contains(p.Instructions, "Формат текущего запроса Solveo имеет приоритет") || !strings.Contains(p.Instructions, "подход — без кода") {
				t.Fatal("developer instruction does not protect approach format")
			}
		}
	}
}

func TestFirstUnknownTurnDoesNotConfirmDurableInstructions(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	r := llm.Request{GlobalInstructions: "initial", Turn: chat.Message{Text: "[crash-before]"}}
	out, err := c.Generate(context.Background(), r, nil)
	if !errors.Is(err, llm.ErrOutcomeUnknown) {
		t.Fatal(err)
	}
	var state sessionState
	_ = json.Unmarshal(out.State, &state)
	if state.InstructionsHash != "" || state.PendingInstructionsHash == "" {
		t.Fatal("thread/start is not a durable instruction-turn confirmation")
	}
}

func TestCrashBeforeSavingInstructionUpdateRestoresRequestedPreferences(t *testing.T) {
	for _, status := range []string{chat.StatusRequesting, chat.StatusInterrupted, chat.StatusUnknown, chat.StatusFailed, chat.StatusCanceled} {
		t.Run(status, func(t *testing.T) {
			var env fakeEnv
			c := fakeClient(t, &env)
			req := llm.Request{Config: chat.Config{Instructions: "PREFERENCE_A"}, Turn: chat.Message{Text: "first"}}
			confirmed, err := c.Generate(context.Background(), req, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.State = confirmed.State
			req.Config.Instructions = "PREFERENCE_B"
			req.Turn.Text = "[crash-after]"
			if _, err = c.Generate(context.Background(), req, nil); !errors.Is(err, llm.ErrOutcomeUnknown) {
				t.Fatalf("expected unknown outcome, got %v", err)
			}
			_ = c.Close(context.Background())
			// Simulate application death before archiving the returned pending state.
			// The archive still has the previous successful state; Codex has B.
			c = fakeClient(t, &env)
			req.State = confirmed.State
			req.Config.Instructions = "PREFERENCE_A"
			req.History = []chat.Message{{Role: chat.RoleUser, Status: status, Text: "[crash-after]", RequestConfig: &chat.Config{Instructions: "PREFERENCE_B"}}}
			req.Turn.Text = "[show-instructions]"
			out, err := c.Generate(context.Background(), req, nil)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.Text, "PREFERENCE_B") || !strings.Contains(out.Text, "PREFERENCE_A") {
				t.Fatalf("stale archive hash suppressed required update after crash: %s", out.Text)
			}
			if count(readLog(t, env.log), "turn/start") != 3 {
				t.Fatal("interrupted question was automatically resubmitted")
			}

			// Once recovery succeeds, an older failed turn must not cause updates
			// on every later question. Include the assistant as real history does.
			req.State = out.State
			req.History = append(req.History,
				chat.Message{Role: chat.RoleUser, Status: chat.StatusComplete, Text: req.Turn.Text},
				chat.Message{Role: chat.RoleAssistant, Status: chat.StatusComplete, Text: out.Text},
			)
			if _, err = c.Generate(context.Background(), req, nil); err != nil {
				t.Fatal(err)
			}
			updates := 0
			for _, entry := range readLog(t, env.log) {
				if entry.Method == "turn/start" && strings.Contains(string(entry.Params), "Обновление инструкций Solveo") {
					updates++
				}
			}
			if updates != 2 {
				t.Fatalf("expected B update and A recovery only, got %d updates", updates)
			}
		})
	}
}
