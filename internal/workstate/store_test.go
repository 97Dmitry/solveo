package workstate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return s
}

func TestStateRoundTrip(t *testing.T) {
	s := open(t)
	st, notice, e := s.Load()
	if e != nil || notice != "" || len(st.Chats) != 0 {
		t.Fatal(st, notice, e)
	}
	want := State{Chats: map[string]Chat{"chat1": {
		Draft:     Draft{Text: "next question", Revision: 7, ReplyToID: "m0", ReplyToKind: "solution"},
		Queue:     []Item{{ID: "b", Label: "Снимок 1"}, {ID: "a", Label: "Снимок 2"}},
		Submitted: &Submitted{MessageID: "m1", Revision: 5},
	}}}
	if e = s.Save(want); e != nil {
		t.Fatal(e)
	}
	got, notice, e := s.Load()
	c := got.Chats["chat1"]
	if e != nil || notice != "" || c.Draft != want.Chats["chat1"].Draft || len(c.Queue) != 2 || c.Queue[0].ID != "b" || *c.Submitted != *want.Chats["chat1"].Submitted {
		t.Fatal(got, notice, e)
	}
}

func TestSaveRejectsInvalidState(t *testing.T) {
	s := open(t)
	queue := make([]Item, MaxQueue+1)
	for i := range queue {
		queue[i] = Item{ID: strings.Repeat("a", i+1)}
	}
	for _, st := range []State{
		{Chats: map[string]Chat{"../x": {}}},
		{Chats: map[string]Chat{"c": {Queue: queue}}},
		{Chats: map[string]Chat{"c": {Queue: []Item{{ID: "a"}}}, "d": {Queue: []Item{{ID: "a"}}}}},
		{Chats: map[string]Chat{"c": {Draft: Draft{Text: strings.Repeat("x", maxDraftBytes+1)}}}},
	} {
		if s.Save(st) == nil {
			t.Fatalf("accepted %v", st.Chats)
		}
	}
}

func TestNewerVersionIsNotOverwritten(t *testing.T) {
	s := open(t)
	p := filepath.Join(s.dir, stateFile)
	if e := os.WriteFile(p, []byte(`{"version":2,"chats":{}}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.Load(); !errors.Is(e, ErrUnsupported) {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(p); string(b) != `{"version":2,"chats":{}}` {
		t.Fatal(string(b))
	}
}

func TestCorruptStateIsSetAside(t *testing.T) {
	for _, data := range []string{"{", `{"version":1,"chats":{"c":{"queue":[{"id":"../x"}]}}}`, `{"chats":{}}`} {
		s := open(t)
		if e := os.WriteFile(filepath.Join(s.dir, stateFile), []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
		st, notice, e := s.Load()
		if e != nil || notice == "" || len(st.Chats) != 0 {
			t.Fatal(data, notice, e)
		}
		matches, _ := filepath.Glob(filepath.Join(s.dir, stateFile+".bad-*"))
		if len(matches) != 1 {
			t.Fatal(matches)
		}
		if b, _ := os.ReadFile(matches[0]); string(b) != data {
			t.Fatal("set-aside copy differs")
		}
		if e = s.Save(State{}); e != nil {
			t.Fatal(e)
		}
	}
}

func TestCommitAndPrune(t *testing.T) {
	s := open(t)
	for _, id := range []string{"keep", "drop", "partial"} {
		p, e := s.CapturePath(id)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(p, []byte("png"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	for _, id := range []string{"keep", "drop"} {
		if _, e := s.Commit(id); e != nil {
			t.Fatal(e)
		}
	}
	if !hasPNG(t, s, "keep") || hasPNG(t, s, "partial") {
		t.Fatal("commit")
	}
	if e := os.WriteFile(filepath.Join(s.dir, ".solveo-leftover"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	if e := s.Prune(map[string]bool{"keep": true}); e != nil {
		t.Fatal(e)
	}
	entries, _ := os.ReadDir(filepath.Join(s.dir, queueDir))
	if len(entries) != 1 || entries[0].Name() != "keep.png" {
		t.Fatal(entries)
	}
	if _, e := os.Stat(filepath.Join(s.dir, ".solveo-leftover")); !os.IsNotExist(e) {
		t.Fatal("temporary document left")
	}
	if _, e := s.CapturePath(`..\x`); e == nil {
		t.Fatal("accepted unsafe id")
	}
}

func TestStreamRoundTrip(t *testing.T) {
	s := open(t)
	if blocks, e := s.LoadStream(); e != nil || blocks != nil {
		t.Fatal(blocks, e)
	}
	at := time.Date(2026, 10, 22, 10, 0, 0, 0, time.FixedZone("x", 3600))
	want := []Block{{ChatID: "c", MessageID: "m", AttemptID: "a", Block: "solution", Text: "partial", UpdatedAt: at}}
	if e := s.SaveStream(want); e != nil {
		t.Fatal(e)
	}
	got, e := s.LoadStream()
	if e != nil || len(got) != 1 || got[0].Text != "partial" || got[0].UpdatedAt.Location() != time.UTC || !got[0].UpdatedAt.Equal(at) {
		t.Fatal(got, e)
	}
	if e = s.SaveStream([]Block{{ChatID: "c", MessageID: "m", AttemptID: "a", Block: "other"}}); e == nil {
		t.Fatal("accepted unknown block")
	}
	if e = s.SaveStream(nil); e != nil {
		t.Fatal(e)
	}
	if got, e = s.LoadStream(); e != nil || len(got) != 0 {
		t.Fatal(got, e)
	}
}

func TestPruneDoesNotDeleteOutsideWorkRoot(t *testing.T) {
	workdir := t.TempDir()
	external := t.TempDir()
	sentinel := filepath.Join(external, "personal-document.txt")
	if e := os.WriteFile(sentinel, []byte("keep me"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(external, filepath.Join(workdir, "queue")); e != nil {
		t.Skipf("symlinks unavailable: %v", e)
	}
	s, e := Open(workdir)
	if e != nil {
		return
	} // Rejecting escaped queue would be safe.
	if _, _, e = s.Load(); e != nil {
		t.Fatal(e)
	}
	_ = s.Prune(map[string]bool{})
	if _, e = os.Stat(sentinel); e != nil {
		t.Fatalf("Prune deleted unrelated external document: %v", e)
	}
}

func TestHasRejectsEscapedQueue(t *testing.T) {
	workdir := t.TempDir()
	external := t.TempDir()
	if e := os.WriteFile(filepath.Join(external, "secret.png"), []byte("screenshot"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(external, filepath.Join(workdir, "queue")); e != nil {
		t.Skipf("symlinks unavailable: %v", e)
	}
	s, e := Open(workdir)
	if e != nil {
		return
	}
	if present, _ := s.Has("secret"); present {
		t.Fatal("Has accepts screenshot outside work root; Path exposes it to provider")
	}
}

func TestFutureVersionWithChangedSchemaIsProtected(t *testing.T) {
	s := open(t)
	future := []byte(`{"version":2,"chats":[]}`)
	path := filepath.Join(s.dir, stateFile)
	if e := os.WriteFile(path, future, 0600); e != nil {
		t.Fatal(e)
	}
	_, notice, e := s.Load()
	if !errors.Is(e, ErrUnsupported) {
		t.Fatalf("newer version treated as corrupt: notice=%q err=%v", notice, e)
	}
	if got, e := os.ReadFile(path); e != nil || string(got) != string(future) {
		t.Fatalf("newer state moved/changed: %s %v", got, e)
	}
}

func hasPNG(t *testing.T, s *Store, id string) bool {
	t.Helper()
	present, err := s.Has(id)
	if err != nil {
		t.Fatal(err)
	}
	return present
}
