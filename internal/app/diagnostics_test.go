package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/settings"
)

type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type fakeDiagnostics struct {
	mu        sync.Mutex
	dir       string
	crash     string
	summaries []string
	err       error
}

func (d *fakeDiagnostics) LogsDir() string       { return d.dir }
func (d *fakeDiagnostics) PreviousCrash() string { return d.crash }
func (d *fakeDiagnostics) Archive(summary string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.summaries = append(d.summaries, summary)
	if d.err != nil {
		return "", d.err
	}
	return filepath.Join(d.dir, "exports", "diagnostics.zip"), nil
}

func setupLogged(t *testing.T, diag *fakeDiagnostics) (*Controller, *fakeProvider, *logBuffer, *fakeHost) {
	t.Helper()
	s, e := history.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	logs := &logBuffer{}
	host := &fakeHost{}
	f := &fakeProvider{started: make(chan llm.Request, 5), finish: make(chan error, 5), store: s}
	deps := Dependencies{Store: s, SettingsStore: settings.Open(s.Dir), Provider: f, Work: openWork(t, s), NativeHost: host, Log: slog.New(slog.NewTextHandler(logs, nil))}
	if diag != nil {
		deps.Diagnostics = diag
	}
	c, e := New(deps)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _, _ = c.DiscardUnsaved(); _ = c.Close(context.Background()) })
	return c, f, logs, host
}

func ask(t *testing.T, c *Controller, f *fakeProvider, text string, result error) {
	t.Helper()
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: c.Snapshot().ActiveChatID, Text: text}); err != nil {
		t.Fatal(err)
	}
	<-f.started
	f.finish <- result
	waitIdle(t, c)
}

func TestSessionLogOmitsContentUntilEnabled(t *testing.T) {
	c, f, logs, _ := setupLogged(t, nil)
	ask(t, c, f, "private-question-marker", nil)
	text := logs.String()
	for _, want := range []string{`msg="request started"`, "origin=composer", "chars=23", `msg="request finished"`, "status=complete", "answer_chars=6"} {
		if !strings.Contains(text, want) {
			t.Fatalf("log lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "private-question-marker") || strings.Contains(text, "answer=answer") {
		t.Fatalf("content logged while disabled:\n%s", text)
	}

	cfg := c.Snapshot().Settings
	cfg.Diagnostics.IncludeContent = true
	if _, err := c.UpdateSettings(cfg); err != nil {
		t.Fatal(err)
	}
	ask(t, c, f, "visible-question-marker", nil)
	text = logs.String()
	if !strings.Contains(text, "include_content=true") || !strings.Contains(text, "question=visible-question-marker") || !strings.Contains(text, "answer=answer") {
		t.Fatalf("content missing while enabled:\n%s", text)
	}
}

func TestSessionLogRecordsFailuresAndShownErrors(t *testing.T) {
	c, f, logs, _ := setupLogged(t, nil)
	ask(t, c, f, "q", errors.New("provider exploded"))
	c.ReportError(errors.New("visible failure"))
	c.publish()
	text := logs.String()
	if !strings.Contains(text, "status=failed") || !strings.Contains(text, `error="provider exploded"`) {
		t.Fatalf("failed request not logged:\n%s", text)
	}
	if strings.Count(text, `error="visible failure"`) != 1 {
		t.Fatalf("shown error must be logged exactly once:\n%s", text)
	}
}

func TestRendererErrorsAreValidatedAndRateLimited(t *testing.T) {
	c, _, logs, _ := setupLogged(t, nil)
	if err := c.ReportRendererError(RendererError{Source: "browser", Kind: "error"}); err == nil {
		t.Fatal("unknown source accepted")
	}
	if err := c.ReportRendererError(RendererError{Source: "main", Kind: "crash"}); err == nil {
		t.Fatal("unknown kind accepted")
	}
	long := strings.Repeat("я", maxRendererMessage+10)
	for range rendererBurst + 5 {
		if err := c.ReportRendererError(RendererError{Source: "audio", Kind: "render", Message: long, Stack: "at App"}); err != nil {
			t.Fatal(err)
		}
	}
	text := logs.String()
	if n := strings.Count(text, `msg="renderer error"`); n != rendererBurst {
		t.Fatalf("logged %d renderer errors, want %d", n, rendererBurst)
	}
	if strings.Contains(text, strings.Repeat("я", maxRendererMessage+1)) {
		t.Fatal("renderer message not truncated")
	}
}

func TestDiagnosticsArchiveAndCrashNotice(t *testing.T) {
	diag := &fakeDiagnostics{dir: t.TempDir(), crash: "crash-1.txt"}
	c, _, logs, host := setupLogged(t, diag)
	cfg := c.Snapshot().Settings
	cfg.GlobalInstructions = "secret instructions"
	if _, err := c.UpdateSettings(cfg); err != nil {
		t.Fatal(err)
	}
	if v := c.Snapshot().Diagnostics; !v.Available || !v.CrashNotice {
		t.Fatalf("diagnostics view %+v", v)
	}
	path, err := c.CreateDiagnosticsArchive()
	if err != nil {
		t.Fatal(err)
	}
	if c.Snapshot().Diagnostics.CrashNotice {
		t.Fatal("archive must clear the crash notice")
	}
	if len(host.folders) != 1 || host.folders[0] != filepath.Dir(path) {
		t.Fatalf("opened %v", host.folders)
	}
	summary := diag.summaries[0]
	if !strings.Contains(summary, "llm_provider=codex") || !strings.Contains(summary, "global_instructions_chars=19") || strings.Contains(summary, "secret instructions") {
		t.Fatalf("summary:\n%s", summary)
	}
	diag.err = errors.New("disk full")
	if _, err := c.CreateDiagnosticsArchive(); err == nil || !strings.Contains(logs.String(), `msg="diagnostics archive failed"`) {
		t.Fatalf("archive failure: %v\n%s", err, logs.String())
	}
	if err := c.OpenLogsFolder(); err != nil || host.folders[len(host.folders)-1] != diag.dir {
		t.Fatalf("logs folder %v %v", err, host.folders)
	}
}

func TestDismissCrashNotice(t *testing.T) {
	c, _, _, _ := setupLogged(t, &fakeDiagnostics{dir: t.TempDir(), crash: "crash-1.txt"})
	c.DismissCrashNotice()
	if c.Snapshot().Diagnostics.CrashNotice {
		t.Fatal("notice still shown")
	}
	plain, _, _, _ := setupLogged(t, nil)
	if v := plain.Snapshot().Diagnostics; v.Available || v.CrashNotice {
		t.Fatalf("view without diagnostics %+v", v)
	}
	if _, err := plain.CreateDiagnosticsArchive(); err == nil {
		t.Fatal("archive without diagnostics")
	}
}
