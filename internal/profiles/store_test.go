package profiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
)

func fixture() Profile {
	now := chat.Now()
	return Profile{ID: chat.ID(), Name: "Go interview", Config: chat.New().Config, Revision: 1, CreatedAt: now, UpdatedAt: now}
}
func TestStoreRoundtripAndValidation(t *testing.T) {
	s := New(t.TempDir())
	list, err := s.Load()
	if err != nil || len(list) != 0 {
		t.Fatalf("%v %v", list, err)
	}
	p := fixture()
	p.Config.Experience = "Built billing"
	p.Config.FastModel = "small"
	p.Config.ParallelAnswers = true
	if err = s.Save([]Profile{p}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || len(got) != 1 || got[0] != p {
		t.Fatalf("%+v %v", got, err)
	}
	for _, bad := range []Profile{func() Profile { v := p; v.Name = " "; return v }(), func() Profile { v := p; v.ID = "../escape"; return v }(), func() Profile { v := p; v.Revision = 0; return v }(), func() Profile { v := p; v.Config.Experience = "\x00"; return v }()} {
		if s.Save([]Profile{bad}) == nil {
			t.Fatal("invalid profile accepted")
		}
	}
	if s.Save([]Profile{p, p}) == nil {
		t.Fatal("duplicate ID accepted")
	}
	if s.Save(make([]Profile, 101)) == nil {
		t.Fatal("limit ignored")
	}
	got, err = s.Load()
	if err != nil || len(got) != 1 || got[0] != p {
		t.Fatal("failed write replaced library")
	}
}
func TestCorruptAndFutureLibraryCannotBeOverwritten(t *testing.T) {
	for _, text := range []string{`bad`, `{"version":2,"profiles":[]}`, `{"version":1,"profiles":[{}]}`} {
		t.Run(text, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "profiles.json")
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			s := New(dir)
			if _, err := s.Load(); err == nil {
				t.Fatal("invalid library accepted")
			}
			if err := s.Save([]Profile{fixture()}); err == nil {
				t.Fatal("invalid library overwritten")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != text {
				t.Fatal("lost original data")
			}
			if err = os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Load(); err != nil {
				t.Fatal(err)
			}
			if err = s.Save([]Profile{fixture()}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestProfileFileEscapeAndReadLimit(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{"version":1,"profiles":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "profiles.json")); err != nil {
		t.Skip(err)
	}
	s := New(dir)
	if _, err := s.Load(); err == nil {
		t.Fatal("followed escaping symlink")
	}
	if err := s.Save([]Profile{fixture()}); err == nil {
		t.Fatal("write after unsafe load")
	}
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "profiles.json"), []byte(strings.Repeat(" ", 32<<20+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir).Load(); err == nil {
		t.Fatal("unbounded read")
	}
}

func TestSaveNormalizesUTCWithoutChangingCaller(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.Load(); err != nil {
		t.Fatal(err)
	}
	p := fixture()
	p.CreatedAt = p.CreatedAt.In(time.FixedZone("local", 6*3600))
	p.UpdatedAt = p.CreatedAt
	list := []Profile{p}
	if err := s.Save(list); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(s.dir, "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "+06:00") {
		t.Fatal("persisted non-UTC times")
	}
	if list[0].CreatedAt.Location().String() != "local" {
		t.Fatal("mutated caller")
	}
}
