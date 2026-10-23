package audiohistory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sample() Session {
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.FixedZone("test", 10800))
	s := Session{ChatID: strings.Repeat("c", 32), ID: strings.Repeat("a", 32), ProviderID: "gemini", ProviderLabel: "Gemini", StartedAt: at, EndedAt: at.Add(time.Minute), Dictionary: []string{"Solveo"}, Sources: []Source{{StreamID: "stream", Track: "system", Device: "default"}}, Errors: []Failure{{At: at, Track: "system", Message: "disconnected"}}, Submissions: []Submission{{At: at, ChatID: "chat", OperationID: "op", SegmentIDs: []string{"stream:0"}}}}
	for i := 0; i < 350; i++ {
		s.Segments = append(s.Segments, Segment{ID: fmt.Sprintf("stream:%d", i), StreamID: "stream", Track: "system", Text: "hello", StartedAt: at, FinalAt: at.Add(2 * time.Minute)})
	}
	return s
}
func TestRoundTripAndClone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new")
	store := New(dir)
	if list, err := store.ForChat(strings.Repeat("c", 32)); err != nil || len(list) != 0 {
		t.Fatalf("empty list %v %v", list, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("ForChat created directory")
	}
	s := sample()
	if err := store.Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Segments) != 350 || got.StartedAt.Location() != time.UTC || got.Segments[0].FinalAt.Location() != time.UTC {
		t.Fatal("lost segments or UTC")
	}
	cloned := Clone(got)
	cloned.Segments[0].Text = "changed"
	cloned.Sources[0].Device = "changed"
	cloned.Dictionary[0] = "changed"
	cloned.Errors[0].Message = "changed"
	cloned.Submissions[0].SegmentIDs[0] = "changed"
	if got.Segments[0].Text != "hello" || got.Sources[0].Device != "default" || got.Dictionary[0] != "Solveo" || got.Errors[0].Message != "disconnected" || got.Submissions[0].SegmentIDs[0] != "stream:0" {
		t.Fatal("clone aliases original")
	}
	list, err := store.ForChat(strings.Repeat("c", 32))
	if err != nil || len(list) != 1 || list[0].SegmentCount != 350 {
		t.Fatalf("list %v %v", list, err)
	}
	if err := store.Delete(s.ID); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("temporary files remain")
	}
}
func TestPreserveInvalidAndImmutableDocuments(t *testing.T) {
	for _, raw := range []string{`{"version":3,"summary":{}}`, `{not json`, ""} {
		t.Run(fmt.Sprintf("bytes%d", len(raw)), func(t *testing.T) {
			dir := t.TempDir()
			s := sample()
			p := filepath.Join(dir, s.ID+".json")
			if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if err := New(dir).Save(s); err == nil {
				t.Fatal("overwrote invalid document")
			}
			got, _ := os.ReadFile(p)
			if string(got) != raw {
				t.Fatal("changed invalid document")
			}
		})
	}
	store := New(t.TempDir())
	s := sample()
	if err := store.Save(s); err != nil {
		t.Fatal(err)
	}
	s.ProviderID = "other"
	if err := store.Save(s); err == nil {
		t.Fatal("changed immutable provider")
	}
	s = sample()
	s.StartedAt = s.StartedAt.Add(time.Second)
	if err := store.Save(s); err == nil {
		t.Fatal("changed immutable start")
	}
}
func TestValidationAndPaths(t *testing.T) {
	cases := map[string]func(*Session){"path": func(s *Session) { s.ID = "../private-key" }, "upper": func(s *Session) { s.ID = strings.Repeat("A", 32) }, "duplicate": func(s *Session) { s.Segments[1].ID = s.Segments[0].ID }, "track": func(s *Session) { s.Segments[0].Track = "audio" }, "nul": func(s *Session) { s.Segments[0].Text = "x\x00" }, "utf8": func(s *Session) { s.Segments[0].Text = string([]byte{255}) }, "empty text": func(s *Session) { s.Segments[0].Text = " " }, "too many": func(s *Session) { s.Segments = make([]Segment, MaxSegments+1) }, "dictionary": func(s *Session) { s.Dictionary = make([]string, 257) }, "errors": func(s *Session) { s.Errors = make([]Failure, 501) }, "submissions": func(s *Session) { s.Submissions = make([]Submission, 1001) }, "sources": func(s *Session) { s.Sources = make([]Source, 3) }, "time": func(s *Session) { s.EndedAt = s.StartedAt.Add(-time.Second) }}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := sample()
			mutate(&s)
			if err := New(t.TempDir()).Save(s); err == nil {
				t.Fatal("accepted invalid session")
			}
		})
	}
	st := New(t.TempDir())
	for _, id := range []string{"../private-key", "", strings.Repeat("A", 32)} {
		if _, err := st.Load(id); err == nil || strings.Contains(err.Error(), "private-key") {
			t.Fatal("unsafe load error", err)
		}
		if err := st.Delete(id); err == nil {
			t.Fatal("unsafe delete")
		}
	}
	s := sample()
	s.Sources = nil
	if err := st.Save(s); err != nil {
		t.Fatal("premature source association requirement", err)
	}
}
func TestForChatReadsHeaderAndSkipsInvalid(t *testing.T) {
	dir := t.TempDir()
	st := New(dir)
	s := sample()
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, s.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	pos := strings.Index(string(data), `"session":`)
	if pos < 0 {
		t.Fatal("missing session")
	}
	// A valid header must be readable without decoding the (potentially huge) body.
	if err := os.WriteFile(filepath.Join(dir, s.ID+".json"), append(data[:pos], []byte(`"session": BROKEN`)...), 0600); err != nil {
		t.Fatal(err)
	}
	list, err := st.ForChat(strings.Repeat("c", 32))
	if err != nil || len(list) != 1 {
		t.Fatalf("read body: %v %v", list, err)
	}
	badID := strings.Repeat("b", 32)
	if err := os.WriteFile(filepath.Join(dir, badID+".json"), []byte(`{"version":3,"summary":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	list, err = st.ForChat(strings.Repeat("c", 32))
	if err == nil || len(list) != 1 || strings.Contains(err.Error(), dir) {
		t.Fatalf("partial list or sanitized error: %v %v", list, err)
	}
	if _, err := st.Load(s.ID); err == nil {
		t.Fatal("Load accepted broken body")
	}
}
func TestSummaryMatchesDocument(t *testing.T) {
	st := New(t.TempDir())
	s := sample()
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(st.dir, s.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var sum Summary
	if err = json.Unmarshal(doc["summary"], &sum); err != nil {
		t.Fatal(err)
	}
	sum.SegmentCount++
	doc["summary"], _ = json.Marshal(sum)
	data, _ = json.Marshal(doc)
	if err = os.WriteFile(filepath.Join(st.dir, s.ID+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Load(s.ID); err == nil {
		t.Fatal("accepted inconsistent summary")
	}
	before, _ := os.ReadFile(filepath.Join(st.dir, s.ID+".json"))
	_ = st.Save(s)
	after, _ := os.ReadFile(filepath.Join(st.dir, s.ID+".json"))
	if !reflect.DeepEqual(before, after) {
		t.Fatal("overwrote inconsistent document")
	}
}

func TestExportedValidationTextBounds(t *testing.T) {
	s := sample()
	if err := Validate(s); err != nil {
		t.Fatal(err)
	}
	row := s.Segments[0]
	row.Text = strings.Repeat("😀", 65536)
	if err := ValidateSegment(row); err != nil {
		t.Fatal(err)
	}
	row.Text += "x"
	if err := ValidateSegment(row); err == nil {
		t.Fatal("accepted excessive code points")
	}
	row = s.Segments[0]
	row.StartedAt = s.StartedAt.Truncate(time.Second)
	s.StartedAt = s.StartedAt.Add(time.Millisecond)
	s.Segments = []Segment{row}
	s.Errors = nil
	s.Submissions = nil
	if err := Validate(s); err != nil {
		t.Fatal("UI timestamp truncation rejected", err)
	}
}
func TestRootRejectsSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	s := sample()
	name := s.ID + ".json"
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("private-value"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, name)); err != nil {
		t.Skip(err)
	}
	st := New(dir)
	if _, err := st.Load(s.ID); err == nil || strings.Contains(err.Error(), outside) {
		t.Fatal("unsafe symlink load", err)
	}
	if err := st.Save(s); err == nil {
		t.Fatal("overwrote escaping symlink")
	}
	if err := st.Delete(s.ID); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(secret)
	if string(data) != "private-value" {
		t.Fatal("changed symlink target")
	}
}
func TestOversizedDocumentsAndConcurrentStores(t *testing.T) {
	dir := t.TempDir()
	s := sample()
	path := filepath.Join(dir, s.ID+".json")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(MaxDocumentBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	st := New(dir)
	if _, err = st.Load(s.ID); err == nil {
		t.Fatal("accepted oversized document")
	}
	if err = st.Save(s); err == nil {
		t.Fatal("replaced oversized document")
	}
	if st.mu != New(dir).mu {
		t.Fatal("stores in same directory do not serialize")
	}
	if err = st.Delete(s.ID); err != nil {
		t.Fatal(err)
	}
}

func TestDocumentBudgetIncludesJSONOverhead(t *testing.T) {
	s := sample()
	s.Segments = nil
	s.Submissions = nil
	text := strings.Repeat("😀", 65536)
	for i := 0; i < 128; i++ {
		s.Segments = append(s.Segments, Segment{ID: fmt.Sprintf("stream:%d", i), StreamID: "stream", Track: "system", Text: text, StartedAt: s.StartedAt, FinalAt: s.StartedAt})
	}
	if err := Validate(s); err == nil {
		t.Fatal("accepted payload whose JSON exceeds the document budget")
	}
}

func TestChatAssociationAndVersionTwo(t *testing.T) {
	st := New(t.TempDir())
	s := sample()
	s.ChatID = strings.Repeat("c", 32)
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load(s.ID)
	if err != nil || got.ChatID != s.ChatID || Clone(got).ChatID != s.ChatID {
		t.Fatalf("chat roundtrip: %v %v", got.ChatID, err)
	}
	raw, err := os.ReadFile(filepath.Join(st.dir, s.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc document
	if err = json.Unmarshal(raw, &doc); err != nil || doc.Version != 2 || doc.Summary.ChatID != s.ChatID {
		t.Fatalf("schema: %+v %v", doc.Summary, err)
	}
	s.ChatID = strings.Repeat("d", 32)
	if err := st.Save(s); err == nil {
		t.Fatal("changed immutable chat association")
	}
	s = sample()
	s.ChatID = "../invalid"
	if err := Validate(s); err == nil {
		t.Fatal("accepted invalid chat")
	}
}
func TestForChatAllParts(t *testing.T) {
	st := New(t.TempDir())
	base := sample()
	base.Segments, base.Errors, base.Submissions = nil, nil, nil
	base.EndedAt = time.Time{}
	for i := 0; i < 205; i++ {
		s := base
		s.ID = fmt.Sprintf("%032x", i)
		s.StartedAt = s.StartedAt.Add(time.Duration(i) * time.Second)
		if err := st.Save(s); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := st.ForChat(base.ChatID)
	if err != nil || len(rows) != 205 {
		t.Fatal(len(rows), err)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].StartedAt.Before(rows[i-1].StartedAt) {
			t.Fatal("not chronological")
		}
	}
	if _, err := st.ForChat("../invalid"); err == nil {
		t.Fatal("accepted invalid chat")
	}
}

func TestOldFormatsAreIgnoredWithoutRewriting(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			st := New(t.TempDir())
			old := sample()
			old.ChatID = ""
			old.Submissions[0].ChatID = strings.Repeat("c", 32)
			normalize(&old)
			raw, err := json.Marshal(document{Version: version, Summary: summarize(old), Session: old})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(st.dir, old.ID+".json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			rows, err := st.ForChat(strings.Repeat("c", 32))
			if err != nil || len(rows) != 0 {
				t.Fatalf("old history shown or warned: %v %v", rows, err)
			}
			if _, err := st.Load(old.ID); err == nil {
				t.Fatal("old format still readable")
			}
			current := sample()
			current.ChatID = strings.Repeat("c", 32)
			if err := st.Save(current); err == nil {
				t.Fatal("old document overwritten")
			}
			after, err := os.ReadFile(path)
			if err != nil || !reflect.DeepEqual(raw, after) {
				t.Fatal("old file changed", err)
			}
			current.ID = strings.Repeat("b", 32)
			if err := st.Save(current); err != nil {
				t.Fatal(err)
			}
			rows, err = st.ForChat(current.ChatID)
			if err != nil || len(rows) != 1 || rows[0].ID != current.ID {
				t.Fatal("old records interfered with new history", rows, err)
			}
		})
	}
}
func TestChatIDIsRequired(t *testing.T) {
	s := sample()
	s.ChatID = ""
	if err := Validate(s); err == nil {
		t.Fatal("accepted history without a chat")
	}
}

func TestVersionOneWithChatIDIsIgnored(t *testing.T) {
	st := New(t.TempDir())
	old := sample()
	normalize(&old)
	raw, err := json.Marshal(document{Version: 1, Summary: summarize(old), Session: old})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.dir, old.ID+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ForChat(old.ChatID)
	if err != nil || len(rows) != 0 {
		t.Fatal("v1 was not ignored", rows, err)
	}
	if _, err := st.Load(old.ID); err == nil {
		t.Fatal("v1 is readable")
	}
}
