package diagnostics

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func clock(start time.Time) func() time.Time {
	var mu sync.Mutex
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		start = start.Add(time.Second)
		return start
	}
}

func open(t *testing.T, o Options) *Session {
	t.Helper()
	if o.Now == nil {
		o.Now = clock(time.Date(2026, 10, 24, 9, 0, 0, 0, time.FixedZone("MSK", 3*3600)))
	}
	s, err := Open(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSessionWritesUTCRecordsAndEnd(t *testing.T) {
	dir := t.TempDir()
	s := open(t, Options{Dir: dir})
	if !strings.HasPrefix(s.Name, "20261024-060001-") {
		t.Fatalf("name %q is not a UTC stamp", s.Name)
	}
	s.Logger.Info("operation finished", "kind", "submit", "err", errors.New("Bearer abcdefghijklmnop"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	text := read(t, s.Path())
	for _, want := range []string{`msg="operation finished" kind=submit err="Bearer [скрыто]"`, `msg="session end"`, "Z level=INFO"} {
		if !strings.Contains(text, want) {
			t.Fatalf("log lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "+03:00") || strings.Contains(text, "abcdefghijklmnop") {
		t.Fatalf("log has local time or a secret:\n%s", text)
	}
	s.Logger.Error("after close")
	if strings.Contains(read(t, s.Path()), "after close") {
		t.Fatal("records after Close must be dropped")
	}
}

func TestOpenKeepsNewestSessionsOnly(t *testing.T) {
	dir := t.TempDir()
	for i := range 25 {
		name := fmt.Sprintf("20261001-0000%02d-%d", i, 100+i)
		if err := os.WriteFile(filepath.Join(dir, sessionLog(name)), []byte("x\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if i%5 == 0 {
			if err := os.WriteFile(filepath.Join(dir, sessionCrash(name)), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	other := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(other, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside.log")
	if err := os.WriteFile(target, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, sessionLog("20200101-000000-1"))
	linked := os.Symlink(target, link) == nil
	s := open(t, Options{Dir: dir, Keep: 20})
	names, _, err := sessionFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 20 || names[0] != s.Name || names[19] != "20261001-000006-106" {
		t.Fatalf("kept %v", names)
	}
	if _, err := os.Lstat(filepath.Join(dir, sessionCrash("20261001-000010-110"))); !os.IsNotExist(err) {
		t.Fatal("empty crash file of a kept session not removed")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("unrelated file removed")
	}
	if linked {
		if _, err := os.Lstat(link); err != nil {
			t.Fatal("link removed")
		}
		if read(t, target) != "outside" {
			t.Fatal("link target changed")
		}
	}
}

func TestOpenReportsPreviousCrashAndUncleanExit(t *testing.T) {
	dir := t.TempDir()
	first := open(t, Options{Dir: dir, Now: clock(time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC))})
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := open(t, Options{Dir: dir})
	if second.PreviousCrash != "" || second.PreviousUnclean {
		t.Fatalf("clean previous session reported as %q/%v", second.PreviousCrash, second.PreviousUnclean)
	}
	// The second session ends without its end record and leaves a crash trace.
	second.Flush(time.Second)
	if err := os.WriteFile(filepath.Join(dir, sessionCrash(second.Name)), []byte("panic: boom"), 0600); err != nil {
		t.Fatal(err)
	}
	third := open(t, Options{Dir: dir, Now: clock(time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC))})
	if third.PreviousCrash != sessionCrash(second.Name) || !third.PreviousUnclean {
		t.Fatalf("got %q/%v", third.PreviousCrash, third.PreviousUnclean)
	}
}

func TestSizeLimitKeepsErrorsAndEndRecord(t *testing.T) {
	s := open(t, Options{Dir: t.TempDir(), MaxBytes: 2000})
	for i := range 100 {
		s.Logger.Info("filler", "i", i)
		s.Flush(time.Second)
	}
	s.Logger.Error("late failure")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	text := read(t, s.Path())
	if strings.Count(text, "log size limit reached") != 1 || !strings.Contains(text, "late failure") || !strings.Contains(text, `msg="session end"`) {
		t.Fatalf("unexpected log:\n%s", text)
	}
	if strings.Contains(text, "i=99") {
		t.Fatal("info records written past the limit")
	}
	if !strings.Contains(text, `msg="log records dropped"`) {
		t.Fatal("dropped records not reported")
	}
}

func TestWriterNeverBlocks(t *testing.T) {
	s := &Session{queue: make(chan []byte, 1)}
	w := writer{s}
	for range 3 {
		if _, err := w.Write([]byte("x\n")); err != nil {
			t.Fatal(err)
		}
	}
	if s.dropped.Load() != 2 {
		t.Fatalf("dropped %d", s.dropped.Load())
	}
}

func TestConcurrentLoggingAndClose(t *testing.T) {
	s := open(t, Options{Dir: t.TempDir()})
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 200 {
				s.Logger.Info("tick", "g", g, "i", i)
			}
		})
	}
	wg.Go(func() { _ = s.Close() })
	wg.Wait()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

// fakeKey builds key-shaped fixtures at run time, so secret scanners never see
// a credential-like literal in the source.
func fakeKey(prefix string, n int) string {
	return prefix + strings.Repeat("x7", n/2)
}

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"Authorization: Bearer abc.def-ghi_jkl":           "Authorization: [скрыто] [скрыто]",
		`{"api_key": "` + fakeKey("xai-", 24) + `"}`:      `{"api_key": "[скрыто]"}`,
		"key " + fakeKey("AI"+"za", 36) + " end":          "key [скрыто] end",
		"plain " + fakeKey("xai-", 24) + " end":           "plain [скрыто] end",
		"token=secret-value&x=1":                          "token=[скрыто]&x=1",
		"chat 0123456789abcdef0123456789abcdef saved":     "chat 0123456789abcdef0123456789abcdef saved",
		"blob " + strings.Repeat("9f86d0", 8):             "blob [скрыто]",
		"max_tokens=1000 and the token budget":            "max_tokens=1000 and the token budget",
		`C:\Users\me\solveo-data\audio-history\file.json`: `C:\Users\me\solveo-data\audio-history\file.json`,
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestArchiveHasSummaryAndNewestSessions(t *testing.T) {
	dir := t.TempDir()
	for i := range 7 {
		name := fmt.Sprintf("20261001-0000%02d-1", i)
		if err := os.WriteFile(filepath.Join(dir, sessionLog(name)), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, sessionCrash("20261001-000006-1")), []byte("panic"), 0600); err != nil {
		t.Fatal(err)
	}
	s := open(t, Options{Dir: dir})
	s.Logger.Info("current session record")
	dest := filepath.Join(t.TempDir(), "exports")
	path, err := s.Archive(dest, "version=test\n")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "diagnostics-20261024-060002.zip" {
		t.Fatalf("archive name %q", path)
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = z.Close() }()
	got := []string{}
	content := map[string]string{}
	for _, f := range z.File {
		got = append(got, f.Name)
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		content[f.Name] = string(b)
	}
	slices.Sort(got)
	want := []string{"crash-20261001-000006-1.txt", sessionLog("20261001-000003-1"), sessionLog("20261001-000004-1"), sessionLog("20261001-000005-1"), sessionLog("20261001-000006-1"), sessionLog(s.Name), "summary.txt"}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("archive has %v, want %v", got, want)
	}
	if content["summary.txt"] != "version=test\n" || !strings.Contains(content[sessionLog(s.Name)], "current session record") {
		t.Fatalf("archive content %v", content)
	}
	entries, _ := os.ReadDir(dest)
	if len(entries) != 1 {
		t.Fatalf("temporary files left: %v", entries)
	}
	if second, err := s.Archive(dest, ""); err != nil || filepath.Base(second) == filepath.Base(path) {
		t.Fatalf("second archive %q, %v", second, err)
	}
}

func TestTailKeepsLastBytes(t *testing.T) {
	tail := &Tail{Limit: 8}
	_, _ = tail.Write([]byte("hello "))
	_, _ = tail.Write([]byte("world!"))
	if got := tail.String(); got != "…o world!" {
		t.Fatalf("tail %q", got)
	}
	short := &Tail{Limit: 64}
	_, _ = short.Write([]byte("panic: boom\n"))
	if short.String() != "panic: boom\n" {
		t.Fatal(short.String())
	}
}
