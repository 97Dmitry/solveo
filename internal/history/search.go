package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/97Dmitry/solveo/internal/chat"
)

const (
	MaxQueryRunes = 200
	snippetRunes  = 60
)

type SearchHit struct {
	ChatID    string    `json:"chat_id"`
	Title     string    `json:"title"`
	Snippet   string    `json:"snippet"`
	Matches   int       `json:"matches"`
	Pinned    bool      `json:"pinned"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Search finds chats whose title, questions or answers contain query,
// ignoring case. It reads every chat document without an index; ctx is
// checked between chats so a newer query can cancel an older one.
func (s *Store) Search(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	needle := fold(strings.TrimSpace(query))
	if len(needle) == 0 || len(needle) > MaxQueryRunes || !utf8.ValidString(query) {
		return nil, errors.New("запрос поиска должен содержать от 1 до 200 символов")
	}
	entries, e := os.ReadDir(filepath.Join(s.Dir, "chats"))
	if e != nil {
		return nil, e
	}
	pins := map[string]bool{}
	if root, e := os.OpenRoot(s.Dir); e == nil {
		pins, _ = s.readPins(root)
		_ = root.Close()
	}
	hits := []SearchHit{}
	for _, f := range entries {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		c, e := s.Load(strings.TrimSuffix(f.Name(), ".json"))
		if e != nil {
			continue
		}
		if hit, ok := match(c, needle); ok {
			hit.Pinned = pins[c.ID]
			hits = append(hits, hit)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Pinned != hits[j].Pinned {
			return hits[i].Pinned
		}
		return hits[i].UpdatedAt.After(hits[j].UpdatedAt)
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// fold lowercases rune by rune, so indexes into the result are indexes into
// the original text's runes. strings.ToLower may change the length.
func fold(text string) []rune {
	r := []rune(text)
	for i, v := range r {
		r[i] = unicode.ToLower(v)
	}
	return r
}
func match(c chat.Chat, needle []rune) (SearchHit, bool) {
	hit := SearchHit{ChatID: c.ID, Title: c.Title, UpdatedAt: c.UpdatedAt}
	visit := func(text string) {
		original := []rune(text)
		folded := fold(text)
		for i := 0; i+len(needle) <= len(folded); {
			if !hasPrefix(folded[i:], needle) {
				i++
				continue
			}
			if hit.Matches == 0 {
				hit.Snippet = snippet(original, i, len(needle))
			}
			hit.Matches++
			i += len(needle)
		}
	}
	visit(c.Title)
	for _, m := range c.Messages {
		visit(m.Text)
		for _, a := range m.Answers {
			visit(a.Text)
		}
	}
	return hit, hit.Matches > 0
}
func hasPrefix(text, prefix []rune) bool {
	for i, r := range prefix {
		if text[i] != r {
			return false
		}
	}
	return true
}
func snippet(text []rune, at, n int) string {
	start := max(0, at-snippetRunes/2)
	end := min(len(text), at+n+snippetRunes/2)
	out := strings.Join(strings.Fields(string(text[start:end])), " ")
	if start > 0 {
		out = "…" + out
	}
	if end < len(text) {
		out += "…"
	}
	return out
}
