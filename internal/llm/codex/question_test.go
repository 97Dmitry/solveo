package codex

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/questionbuilder"
)

func TestQuestionAnalysisIsolatedFromMainConversation(t *testing.T) {
	env := fakeEnv{}
	c := fakeClient(t, &env)
	in := questionbuilder.Input{ChatID: "local-chat", Segments: []questionbuilder.Segment{{ID: "s", Track: "system", Text: "Explain channels", Final: true}}}
	result, err := c.AnalyzeQuestion(context.Background(), in, chat.Config{Model: "gpt-test", Reasoning: "low"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ready || result.Text != "Explain channels" {
		t.Fatal(result)
	}
	out, err := c.Generate(context.Background(), request("ordinary"), nil)
	if err != nil || len(out.State) == 0 {
		t.Fatal(out, err)
	}
	lines := readLog(t, env.log)
	if count(lines, "start") != 2 {
		t.Fatal("analysis shares main process")
	}
	var analysisStart, analysisTurn map[string]any
	for _, l := range lines {
		var p map[string]any
		_ = json.Unmarshal(l.Params, &p)
		if l.Method == "thread/start" && p["baseInstructions"] != nil {
			analysisStart = p
		}
		if l.Method == "turn/start" && p["outputSchema"] != nil {
			analysisTurn = p
		}
	}
	if analysisStart["ephemeral"] != true || analysisTurn == nil {
		t.Fatal(analysisStart, analysisTurn)
	}
	b, _ := json.Marshal(analysisStart)
	if strings.Contains(string(b), "Основное решение") {
		t.Fatal("answer instructions leaked")
	}
}
func TestQuestionCancellationKeepsMainTransport(t *testing.T) {
	c := fakeClient(t, &fakeEnv{})
	_, err := c.Generate(context.Background(), request("main"), nil)
	if err != nil {
		t.Fatal(err)
	}
	c.app.mu.Lock()
	conn := c.app.conn
	c.app.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = c.AnalyzeQuestion(ctx, questionbuilder.Input{Segments: []questionbuilder.Segment{{ID: "s", Track: "system", Text: "[slow]", Final: true}}}, chat.Config{Model: "gpt-test"}, nil)
	if err == nil {
		t.Fatal("not cancelled")
	}
	if conn.Err() != nil {
		t.Fatal("main connection stopped", conn.Err())
	}
}

func TestQuestionResultRequiresAllFields(t *testing.T) {
	for _, text := range []string{`{}`, `{"has_question":false,"ready":false,"text":"","source_ids":[],"extra":true}`, `{"has_question":false,"ready":false,"text":"","source_ids":null,"follow_up":false}`, `{"has_question":false,"ready":false,"text":"","source_ids":[],"follow_up":false} {}`} {
		if _, err := decodeQuestionResult(text); err == nil {
			t.Fatalf("accepted %s", text)
		}
	}
	if _, err := decodeQuestionResult(`{"has_question":false,"ready":false,"text":"","source_ids":[],"follow_up":false}`); err != nil {
		t.Fatal(err)
	}
}

func TestQuestionFollowupQuotesPreviousUserQuestion(t *testing.T) {
	c := &Client{}
	r := request("clarification")
	r.Turn.Question = &questionbuilder.Card{PreviousQuestionID: "old"}
	r.History = []chat.Message{{ID: "old", Role: chat.RoleUser, Text: "original conditions"}}
	prompt := c.prompt(r)
	if !strings.Contains(prompt, `"referenced_question":{"message_id":"old","text":"original conditions"}`) {
		t.Fatal(prompt)
	}
}

// Removing the analysis process restrictions or reintroducing a table override
// must fail this test: thread-level tables supersede startup dotted overrides.
func TestQuestionIsolationUsesProcessFlagsWithoutThreadTableReplacement(t *testing.T) {
	env := fakeEnv{}
	c := fakeClient(t, &env)
	_, err := c.AnalyzeQuestion(context.Background(), questionbuilder.Input{Segments: []questionbuilder.Segment{{ID: "s", Track: "system", Text: "question", Final: true}}}, chat.Config{Model: "gpt-test", Reasoning: "low"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var starts []logLine
	var params map[string]json.RawMessage
	for _, line := range readLog(t, env.log) {
		if line.Start != nil {
			starts = append(starts, line)
		}
		if line.Method == "thread/start" {
			if err = json.Unmarshal(line.Params, &params); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(starts) != 1 {
		t.Fatalf("process starts=%d", len(starts))
	}
	flags := map[string]bool{}
	overrides := map[string]string{}
	args := starts[0].Start
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--disable" {
			flags[args[i+1]] = true
		}
		if args[i] == "-c" {
			k, v, ok := strings.Cut(args[i+1], "=")
			if ok {
				overrides[k] = v
			}
		}
	}
	for _, key := range []string{"apps", "plugins", "remote_plugin", "browser_use", "computer_use", "shell_tool", "view_image", "hooks", "tool_suggest"} {
		if !flags[key] {
			t.Errorf("analysis startup does not disable %s", key)
		}
	}
	for key, value := range map[string]string{"web_search": `"disabled"`, "tools.view_image": "false", "tools.update_plan.enabled": "false", "tools.experimental_request_user_input.enabled": "false"} {
		if overrides[key] != value {
			t.Errorf("analysis startup override %s=%q, want %q", key, overrides[key], value)
		}
	}
	var cfg map[string]json.RawMessage
	if err = json.Unmarshal(params["config"], &cfg); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"features", "tools"} {
		if _, ok := cfg[key]; ok {
			t.Errorf("thread replaces startup %s table", key)
		}
	}
}

// The compact question from the real regression no longer contains 'write'.
// The answering model must still receive that explicit condition as source data.
func TestAutomaticQuestionRetainsOriginalConditionsInAnswerPayload(t *testing.T) {
	c := &Client{}
	for _, purpose := range []string{"", chat.AnswerSolution, chat.AnswerApproach} {
		t.Run(purpose, func(t *testing.T) {
			r := request("Как называется одновременное обращение горутин к памяти?")
			r.Purpose = purpose
			r.Turn.FromTranscript = true
			r.Turn.Question = &questionbuilder.Card{Origin: questionbuilder.OriginAutomatic, ID: "q", Version: 1, Text: r.Turn.Text, Sources: []questionbuilder.Segment{{ID: "s", Track: "system", Text: "Горутины будут писать в одну мапу без синхронизации. Как называется этот феномен?", Final: true}}}
			text := c.prompt(r)
			var wire struct {
				Question string                             `json:"question"`
				Sources  []struct{ ID, Track, Text string } `json:"question_sources"`
			}
			if err := json.NewDecoder(strings.NewReader(text)).Decode(&wire); err != nil {
				t.Fatal(err)
			}
			if wire.Question != r.Turn.Text || len(wire.Sources) != 1 || wire.Sources[0].Text != r.Turn.Question.Sources[0].Text {
				t.Fatalf("source condition lost: %+v", wire)
			}
		})
	}
}

func TestAutomaticFollowupRetainsOriginalPreviousConditions(t *testing.T) {
	c := &Client{}
	r := request("Как найти эту проблему?")
	r.Purpose = chat.AnswerApproach
	r.Turn.Question = &questionbuilder.Card{Origin: questionbuilder.OriginAutomatic, PreviousQuestionID: "old"}
	original := "Горутины пишут в общую мапу без синхронизации"
	r.History = []chat.Message{{ID: "old", Role: chat.RoleUser, Text: "Обращение к памяти", Question: &questionbuilder.Card{Origin: questionbuilder.OriginAutomatic, Sources: []questionbuilder.Segment{{ID: "s", Track: "system", Text: original, Final: true}}}}}
	var wire struct {
		Previous struct {
			Sources []struct{ Text string } `json:"source_transcript"`
		} `json:"referenced_question"`
	}
	if err := json.NewDecoder(strings.NewReader(c.prompt(r))).Decode(&wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Previous.Sources) != 1 || wire.Previous.Sources[0].Text != original {
		t.Fatalf("previous conditions lost: %+v", wire)
	}
}

func TestManualAndLegacyQuestionsDoNotRestoreRemovedConditions(t *testing.T) {
	for _, origin := range []string{questionbuilder.OriginManual, ""} {
		t.Run(origin, func(t *testing.T) {
			r := request("Рассмотрим только одновременное чтение")
			oldText := "Горутины пишут в общую мапу без синхронизации"
			old := &questionbuilder.Card{Origin: questionbuilder.OriginAutomatic, Sources: []questionbuilder.Segment{{ID: "s", Track: "system", Text: oldText, Final: true}}}
			r.Turn.Question = &questionbuilder.Card{Origin: origin, Text: r.Turn.Text, Sources: old.Sources, PreviousQuestionID: "old"}
			r.History = []chat.Message{{ID: "old", Role: chat.RoleUser, Text: "Прежний вопрос", Question: old}}
			var wire struct {
				Question string            `json:"question"`
				Sources  []json.RawMessage `json:"question_sources"`
				Previous struct {
					Sources []json.RawMessage `json:"source_transcript"`
				} `json:"referenced_question"`
			}
			prompt := (&Client{}).prompt(r)
			if err := json.NewDecoder(strings.NewReader(prompt)).Decode(&wire); err != nil {
				t.Fatal(err)
			}
			if wire.Question != r.Turn.Text || len(wire.Sources) > 0 || len(wire.Previous.Sources) > 0 || strings.Contains(prompt, oldText) {
				t.Fatal("old conditions restored", prompt)
			}
		})
	}
}

func TestOversizedQuestionEvidenceRejectedBeforeProviderTurn(t *testing.T) {
	env := fakeEnv{}
	c := fakeClient(t, &env)
	if err := c.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := request("question")
	r.Turn.Question = &questionbuilder.Card{Origin: questionbuilder.OriginAutomatic, Sources: []questionbuilder.Segment{{ID: "s", Track: "system", Text: strings.Repeat("я", 16001), Final: true}}}
	if _, err := c.Generate(context.Background(), r, nil); err == nil {
		t.Fatal("oversized source evidence accepted")
	}
	if count(readLog(t, env.log), "turn/start") != 0 {
		t.Fatal("provider turn started for oversized evidence")
	}
}

func TestQuestionSourcesKeepUtteranceOrderForCorrections(t *testing.T) {
	r := request("current question")
	r.Turn.Question = &questionbuilder.Card{Origin: questionbuilder.OriginAutomatic, Sources: []questionbuilder.Segment{{ID: "new", Seq: 2, Track: "system", Text: "Correction: 20", Final: true}, {ID: "old", Seq: 1, Track: "system", Text: "Initially: 10", Final: true}}}
	var wire struct {
		Sources []struct {
			ID  string
			Seq uint64
		} `json:"question_sources"`
	}
	if err := json.NewDecoder(strings.NewReader((&Client{}).prompt(r))).Decode(&wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Sources) != 2 || wire.Sources[0].ID != "old" || wire.Sources[0].Seq != 1 || wire.Sources[1].Seq != 2 {
		t.Fatal("correction order lost", wire)
	}
}

func prepareAnalysis(t *testing.T, c *Client, cfg chat.Config) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- c.PrepareQuestionAnalysis(ctx, cfg) }()
	deadline := time.After(5 * time.Second)
	for {
		c.analysis.mu.Lock()
		p := c.analysis.prepared
		c.analysis.mu.Unlock()
		if p != nil {
			select {
			case <-p.ready:
				return done
			default:
			}
		}
		select {
		case <-deadline:
			t.Fatal("analysis preparation did not become ready")
		case <-time.After(time.Millisecond):
		}
	}
}

type threadUse struct {
	starts          int
	turns, released []string
}

// analysisThreads counts analysis thread starts and lists turns and releases by thread ID.
func analysisThreads(t *testing.T, env fakeEnv) threadUse {
	t.Helper()
	var u threadUse
	for _, l := range readLog(t, env.log) {
		var p map[string]any
		_ = json.Unmarshal(l.Params, &p)
		thread, _ := p["threadId"].(string)
		switch {
		case l.Method == "thread/start" && p["baseInstructions"] == questionInstructions:
			u.starts++
		case l.Method == "turn/start" && p["outputSchema"] != nil:
			u.turns = append(u.turns, thread)
		case l.Method == "thread/unsubscribe":
			u.released = append(u.released, thread)
		}
	}
	return u
}

func TestPreparedQuestionAnalysisSkipsThreadStartAndIsUsedOnce(t *testing.T) {
	env := fakeEnv{}
	c := fakeClient(t, &env)
	cfg := chat.Config{Model: "gpt-test", Reasoning: "low"}
	done := prepareAnalysis(t, c, cfg)
	c.analysis.mu.Lock()
	prepared := c.analysis.prepared.id
	c.analysis.mu.Unlock()
	if u := analysisThreads(t, env); u.starts != 1 || len(u.turns) != 0 {
		t.Fatalf("preparation must start one thread and no turn: %+v", u)
	}
	in := questionbuilder.Input{Segments: []questionbuilder.Segment{{ID: "s", Track: "system", Text: "Explain channels", Final: true}}}
	var timing []string
	if _, err := c.AnalyzeQuestion(context.Background(), in, cfg, func(e llm.Event) { timing = append(timing, e.Text) }); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("claimed preparation did not return")
	}
	u := analysisThreads(t, env)
	if u.starts != 1 || len(u.turns) != 1 || u.turns[0] != prepared {
		t.Fatalf("analysis did not use the prepared thread %s: %+v", prepared, u)
	}
	if !slices.Contains(u.released, prepared) {
		t.Fatal("ephemeral prepared thread was not released", u.released)
	}
	if len(timing) != 1 || !strings.Contains(timing[0], "purpose=question-analysis") || !strings.Contains(timing[0], "prepared_thread=true") {
		t.Fatal(timing)
	}
	// Without a new preparation the next analysis starts its own thread, never reusing the claimed one.
	if _, err := c.AnalyzeQuestion(context.Background(), in, cfg, nil); err != nil {
		t.Fatal(err)
	}
	if u = analysisThreads(t, env); u.starts != 2 || len(u.turns) != 2 || u.turns[1] == prepared {
		t.Fatalf("%+v", u)
	}
}

func TestStaleQuestionPreparationIsReleasedBeforeAnalysis(t *testing.T) {
	env := fakeEnv{}
	c := fakeClient(t, &env)
	done := prepareAnalysis(t, c, chat.Config{Model: "gpt-old"})
	c.analysis.mu.Lock()
	prepared := c.analysis.prepared.id
	c.analysis.mu.Unlock()
	in := questionbuilder.Input{Segments: []questionbuilder.Segment{{ID: "s", Track: "system", Text: "Explain channels", Final: true}}}
	if _, err := c.AnalyzeQuestion(context.Background(), in, chat.Config{Model: "gpt-test"}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stale preparation reported a claim")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stale preparation did not stop")
	}
	u := analysisThreads(t, env)
	if u.starts != 2 || len(u.turns) != 1 || u.turns[0] == prepared || !slices.Contains(u.released, prepared) {
		t.Fatalf("%+v", u)
	}
}
