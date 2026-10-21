package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/llm/codex/appserver/appservertest"
)

func TestMain(m *testing.M) {
	if os.Getenv(appservertest.EnvFake) != "" {
		appservertest.Main()
	}
	os.Exit(m.Run())
}

type fakeEnv struct {
	home, work, store, log string
}

func fakeClient(t *testing.T, env *fakeEnv) *Client {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if env.store == "" {
		dir := t.TempDir()
		*env = fakeEnv{home: filepath.Join(dir, "home"), work: filepath.Join(dir, "work"), store: filepath.Join(dir, "store"), log: filepath.Join(dir, "log.jsonl")}
	}
	t.Setenv(appservertest.EnvFake, "1")
	t.Setenv(appservertest.EnvStore, env.store)
	t.Setenv(appservertest.EnvLog, env.log)
	c := New(exe, "", 10*time.Second, env.home, env.work)
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

type logLine struct {
	Start     []string        `json:"start"`
	CodexHome string          `json:"codex_home"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
}

func readLog(t *testing.T, path string) []logLine {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []logLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var l logLine
		if json.Unmarshal(sc.Bytes(), &l) == nil {
			out = append(out, l)
		}
	}
	return out
}

func count(lines []logLine, method string) int {
	n := 0
	for _, l := range lines {
		if method == "start" && l.Start != nil || l.Method == method {
			n++
		}
	}
	return n
}

type events struct {
	mu   sync.Mutex
	list []llm.Event
}

func (e *events) add(v llm.Event) { e.mu.Lock(); e.list = append(e.list, v); e.mu.Unlock() }
func (e *events) answers() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, v := range e.list {
		if v.Kind == llm.EventAnswerText {
			out = append(out, v.Text)
		}
	}
	return out
}

func request(text string) llm.Request {
	return llm.Request{Turn: chat.Message{Text: text}, ConfigRevision: 1, Config: chat.Config{Model: "gpt-test", Reasoning: "low"}}
}

func decodeState(t *testing.T, raw json.RawMessage) sessionState {
	t.Helper()
	var s sessionState
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("state %s: %v", raw, err)
	}
	return s
}

func TestNewChatStreamsAndReturnsThreadState(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	var seen events
	r, err := c.Generate(context.Background(), request("первый вопрос"), seen.add)
	if err != nil || !strings.Contains(r.Text, "первый вопрос") {
		t.Fatalf("result %+v err %v", r, err)
	}
	s := decodeState(t, r.State)
	if s.Version != 2 || s.Transport != transportAppServer || !validThreadID(s.ThreadID) {
		t.Fatalf("state %+v", s)
	}
	if a := seen.answers(); len(a) == 0 || a[len(a)-1] != r.Text {
		t.Fatalf("previews %q", a)
	}
	lines := readLog(t, env.log)
	if lines[0].CodexHome != env.home {
		t.Fatalf("CODEX_HOME %q", lines[0].CodexHome)
	}
	args := strings.Join(lines[0].Start, " ")
	for _, want := range []string{"app-server", "--disable plugins", "--disable remote_plugin", "--disable apps", `cli_auth_credentials_store="keyring"`, "project_doc_max_bytes=0", `model_provider="openai"`} {
		if !strings.Contains(args, want) {
			t.Fatalf("args %q missing %q", args, want)
		}
	}
	for _, l := range lines {
		if l.Method == "thread/start" || l.Method == "turn/start" {
			var p map[string]any
			_ = json.Unmarshal(l.Params, &p)
			if l.Method == "thread/start" && (p["sandbox"] != "read-only" || p["approvalPolicy"] != "never" || p["cwd"] != env.work || p["ephemeral"] != false) {
				t.Fatalf("thread/start %v", p)
			}
			if l.Method == "turn/start" && (p["effort"] != "low" || p["model"] != "gpt-test") {
				t.Fatalf("turn/start %v", p)
			}
		}
	}
}

func TestResumesExactThreadAfterRestartAndRejectsMissing(t *testing.T) {
	var env fakeEnv
	first := fakeClient(t, &env)
	r, err := first.Generate(context.Background(), request("вопрос"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := fakeClient(t, &env)
	req := request("уточнение")
	req.State = r.State
	again, err := second.Generate(context.Background(), req, nil)
	if err != nil || decodeState(t, again.State).ThreadID != decodeState(t, r.State).ThreadID {
		t.Fatalf("resume %+v %v", again, err)
	}
	lines := readLog(t, env.log)
	if count(lines, "start") != 2 || count(lines, "thread/resume") != 1 || count(lines, "thread/start") != 1 {
		t.Fatalf("starts=%d resumes=%d threads=%d", count(lines, "start"), count(lines, "thread/resume"), count(lines, "thread/start"))
	}
	// A loaded thread is continued without another resume.
	if _, err = second.Generate(context.Background(), req, nil); err != nil || count(readLog(t, env.log), "thread/resume") != 1 {
		t.Fatalf("loaded thread resumed again: %v", err)
	}

	missing, _ := json.Marshal(sessionState{Version: 2, Transport: transportAppServer, ThreadID: "01a10000-0000-7000-8000-000000000000"})
	req.State = missing
	out, err := second.Generate(context.Background(), req, nil)
	if err == nil || !strings.Contains(err.Error(), "не найдена") || string(out.State) != string(missing) {
		t.Fatalf("missing thread: %+v %v", out, err)
	}
	if n := count(readLog(t, env.log), "thread/start"); n != 1 {
		t.Fatalf("missing thread silently started a new one (%d thread/start)", n)
	}
}

func TestIsolationDisablesDiscoveredSkillsOnce(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	t.Setenv(appservertest.EnvSkills, "/u/.agents/skills/a/SKILL.md|/h/skills/.system/b/SKILL.md")
	if err := c.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	lines := readLog(t, env.log)
	if count(lines, "skills/config/write") != 2 || count(lines, "turn/start") != 0 || count(lines, "thread/start") != 0 {
		t.Fatalf("prepare: writes=%d turns=%d threads=%d", count(lines, "skills/config/write"), count(lines, "turn/start"), count(lines, "thread/start"))
	}
}

func TestApproachUsesEphemeralThreadWithoutState(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	req := request("подход")
	req.Purpose = chat.AnswerApproach
	r, err := c.Generate(context.Background(), req, nil)
	if err != nil || len(r.State) != 0 {
		t.Fatalf("approach %+v %v", r, err)
	}
	lines := readLog(t, env.log)
	for _, l := range lines {
		if l.Method == "thread/start" && !strings.Contains(string(l.Params), `"ephemeral":true`) {
			t.Fatalf("approach thread is persistent: %s", l.Params)
		}
	}
	if count(lines, "thread/unsubscribe") != 1 {
		t.Fatal("approach thread was not released")
	}
}

func TestLoginWithoutLeakingAccountDetails(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	t.Setenv(appservertest.EnvNoAccount, "1")
	if _, err := c.Generate(context.Background(), request("q"), nil); err == nil || !strings.Contains(err.Error(), "войдите") {
		t.Fatalf("signed out: %v", err)
	}
	a, err := c.Account(context.Background())
	if err != nil || a.SignedIn {
		t.Fatalf("account %+v %v", a, err)
	}
	done := make(chan error, 1)
	prompt, err := c.StartLogin(context.Background(), func(e error) { done <- e })
	if err != nil || prompt.Code != "ABCD-1234" || prompt.URL != "https://auth.example.test/device" || prompt.ID == "" {
		t.Fatalf("prompt %+v %v", prompt, err)
	}
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("login did not complete")
	}
	if err != nil {
		t.Fatal(err)
	}
	a, err = c.Account(context.Background())
	if err != nil || !a.SignedIn || a.Kind != "chatgpt" {
		t.Fatalf("account %+v %v", a, err)
	}
	if b, _ := json.Marshal(a); strings.Contains(string(b), "@") {
		t.Fatalf("account details leaked: %s", b)
	}
	if _, err = c.Generate(context.Background(), request("после входа"), nil); err != nil {
		t.Fatal(err)
	}
}

func TestCancelOneOfTwoConcurrentRequestsSharesOneProcess(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	ctx, cancel := context.WithCancel(context.Background())
	slow := make(chan error, 1)
	var slowSeen events
	go func() {
		r, err := c.Generate(ctx, request("[slow] длинный"), slowSeen.add)
		if err == nil || len(r.State) == 0 {
			err = errors.New("slow request lost its state")
		}
		slow <- err
	}()
	worker := *c // a copy, as Generate callers may hold, shares the connection
	if _, err := worker.Generate(context.Background(), request("короткий"), nil); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); len(slowSeen.answers()) == 0; {
		if time.Now().After(deadline) {
			t.Fatal("slow request did not stream")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	err := <-slow
	if !errors.Is(err, context.Canceled) || errors.Is(err, llm.ErrOutcomeUnknown) {
		t.Fatalf("cancel: %v", err)
	}
	if _, err = c.Generate(context.Background(), request("после отмены"), nil); err != nil {
		t.Fatal(err)
	}
	if n := count(readLog(t, env.log), "start"); n != 1 {
		t.Fatalf("expected one process, got %d", n)
	}
}

func TestCrashIsUnknownAndNextRequestRestartsWithoutResubmitting(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	first, err := c.Generate(context.Background(), request("начало"), nil)
	if err != nil {
		t.Fatal(err)
	}
	req := request("[crash-after] упадёт")
	req.State = first.State
	var seen events
	r, err := c.Generate(context.Background(), req, seen.add)
	if !errors.Is(err, llm.ErrOutcomeUnknown) || string(r.State) != string(first.State) || r.Text != "" {
		t.Fatalf("crash: %+v %v", r, err)
	}
	if len(seen.answers()) == 0 {
		t.Fatal("partial text was not streamed before the crash")
	}
	next := request("следующий")
	next.State = first.State
	if _, err = c.Generate(context.Background(), next, nil); err != nil {
		t.Fatal(err)
	}
	lines := readLog(t, env.log)
	crashed := 0
	for _, l := range lines {
		if l.Method == "turn/start" && strings.Contains(string(l.Params), "crash-after") {
			crashed++
		}
	}
	if count(lines, "start") != 2 || crashed != 1 || count(lines, "thread/resume") != 1 {
		t.Fatalf("starts=%d crashed-turns=%d resumes=%d", count(lines, "start"), crashed, count(lines, "thread/resume"))
	}
}

func TestLoginCompletionBeforeRegistrationIsNotLost(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	t.Setenv(appservertest.EnvNoAccount, "1")
	t.Setenv(appservertest.EnvLoginEarly, "1")
	done := make(chan error, 1)
	if _, err := c.StartLogin(context.Background(), func(e error) { done <- e }); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("early login completion was lost")
	}
}

func catalog(t *testing.T, pages string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(p, []byte(pages), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(appservertest.EnvModels, p)
}

func TestModelsPaginateThroughSharedConnectionWithoutTurns(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	catalog(t, `[
		{"data":[{"id":"p0","model":"model-0","displayName":"Model 0","hidden":false,"isDefault":true,"defaultReasoningEffort":"adaptive","supportedReasoningEfforts":[{"reasoningEffort":"adaptive","description":"Server"},{"reasoningEffort":"none","description":"No"}]},{"model":"hidden","displayName":"H","hidden":true}],"nextCursor":"1"},
		{"data":[{"id":"p1","model":"model-1","displayName":"Model 1","supportedReasoningEfforts":[]}],"nextCursor":null}
	]`)
	models, err := c.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "model-0" || !models[0].IsDefault || models[0].DefaultReasoning != "adaptive" || models[0].ReasoningEfforts[0].ID != "adaptive" || models[1].ID != "model-1" {
		t.Fatalf("catalog %+v", models)
	}
	lines := readLog(t, env.log)
	if count(lines, "model/list") != 2 || count(lines, "thread/start") != 0 || count(lines, "turn/start") != 0 {
		t.Fatalf("model/list=%d thread/start=%d turn/start=%d", count(lines, "model/list"), count(lines, "thread/start"), count(lines, "turn/start"))
	}
	// The catalog shares the process used for chats.
	if _, err = c.Generate(context.Background(), request("вопрос"), nil); err != nil || count(readLog(t, env.log), "start") != 1 {
		t.Fatalf("second process or error: %v", err)
	}
}

func TestModelsRejectMalformedCatalogAndHideServerErrors(t *testing.T) {
	for _, pages := range []string{
		`[{"nextCursor":null}]`,
		`[{"data":[],"nextCursor":null}]`,
		`[{"data":[{"model":"x","displayName":"X","defaultReasoningEffort":"high","supportedReasoningEfforts":[]}],"nextCursor":null}]`,
		`[{"data":[{"model":"x","displayName":"X","supportedReasoningEfforts":[{"reasoningEffort":"high\nmodel=bad"}]}],"nextCursor":null}]`,
		`[{"data":[{"model":"x","displayName":"X","supportedReasoningEfforts":[]}],"nextCursor":"0"}, {"data":[],"nextCursor":"0"}]`,
		`{"code":-32000,"message":"secret@example.com is not allowed"}`,
	} {
		t.Run(pages, func(t *testing.T) {
			var env fakeEnv
			c := fakeClient(t, &env)
			catalog(t, pages)
			_, err := c.Models(context.Background())
			if err == nil || strings.Contains(err.Error(), "@") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestCheckUsesOneOffSessionAndShortPrompt(t *testing.T) {
	var env fakeEnv
	c := fakeClient(t, &env)
	out, err := c.Check(context.Background(), chat.Config{Model: "gpt-test", Reasoning: "low"}, nil)
	if err != nil || out.Text != "Ответ: Ответь одним словом: OK." {
		t.Fatalf("check %+v %v", out, err)
	}
	lines := readLog(t, env.log)
	for _, l := range lines {
		if l.Method == "thread/start" && !strings.Contains(string(l.Params), `"ephemeral":true`) {
			t.Fatalf("check thread is persistent: %s", l.Params)
		}
	}
	if count(lines, "thread/unsubscribe") != 1 {
		t.Fatal("check thread was not released")
	}
}
