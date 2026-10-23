// Package history persists chat documents and attachments inside a bounded root.
package history

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/storage"
)

const maxChatBytes = 32 << 20

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)

type Source struct{ AttachmentID, TempPath string }
type SaveInput struct {
	Chat    chat.Chat
	Sources []Source
}
type Summary struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	ProviderID string    `json:"provider_id"`
	UpdatedAt  time.Time `json:"updated_at"`
	Prepared   bool      `json:"prepared"`
	Pinned     bool      `json:"pinned"`
}
type Store struct {
	Dir string
	mu  sync.Mutex
}

func Open(dir string) (*Store, error) {
	dir, e := filepath.Abs(dir)
	if e != nil {
		return nil, e
	}
	for _, n := range []string{"chats", "images"} {
		if e = os.MkdirAll(filepath.Join(dir, n), 0700); e != nil {
			return nil, e
		}
	}
	s := &Store{Dir: dir}
	s.sweepTrash()
	// Exports interrupted before their final rename are never shown to the user.
	if entries, e := os.ReadDir(filepath.Join(dir, exportsDir)); e == nil {
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".tmp-") {
				_ = os.RemoveAll(filepath.Join(dir, exportsDir, entry.Name()))
			}
		}
	}
	return s, nil
}

// sweepTrash finishes deletions interrupted between moving a chat's images to
// trash and removing its document: images of a chat that still exists are
// restored, the rest are removed. It is best effort; leftovers are retried on
// the next start.
func (s *Store) sweepTrash() {
	entries, e := os.ReadDir(filepath.Join(s.Dir, "trash"))
	if e != nil {
		return
	}
	root, e := os.OpenRoot(s.Dir)
	if e != nil {
		return
	}
	defer root.Close()
	for _, entry := range entries {
		id, _, ok := strings.Cut(entry.Name(), ".")
		if !ok || !idPattern.MatchString(id) {
			continue
		}
		name := "trash/" + entry.Name()
		if _, e := root.Lstat("chats/" + id + ".json"); e == nil {
			if _, e := root.Lstat("images/" + id); errors.Is(e, os.ErrNotExist) {
				_ = root.Rename(name, "images/"+id)
			}
			continue
		}
		_ = root.RemoveAll(name)
	}
}
func validPath(id, p string) bool {
	return p != "" && !strings.ContainsAny(p, `\:`) && path.Clean(p) == p && strings.HasPrefix(p, "images/"+id+"/") && path.Base(p) != "." && strings.Count(p, "/") == 2
}
func (s *Store) Save(input SaveInput) (chat.Chat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := chat.Clone(input.Chat)
	if !idPattern.MatchString(c.ID) {
		return input.Chat, errors.New("некорректный ID чата")
	}
	root, e := os.OpenRoot(s.Dir)
	if e != nil {
		return input.Chat, e
	}
	defer root.Close()
	committed := false
	copied := []string{}
	defer func() {
		if !committed {
			for _, p := range copied {
				root.Remove(p)
			}
		}
	}()
	sources := map[string]string{}
	for _, src := range input.Sources {
		if !idPattern.MatchString(src.AttachmentID) {
			return input.Chat, errors.New("некорректный ID снимка")
		}
		sources[src.AttachmentID] = src.TempPath
	}
	seen := map[string]bool{}
	for i, a := range c.Attachments {
		if !idPattern.MatchString(a.ID) || seen[a.ID] {
			return input.Chat, errors.New("некорректный или повторяющийся ID снимка")
		}
		seen[a.ID] = true
		if src, ok := sources[a.ID]; ok {
			if e = root.MkdirAll("images/"+c.ID, 0700); e != nil {
				return input.Chat, e
			}
			p := "images/" + c.ID + "/" + a.ID + ".png"
			in, e := os.Open(src)
			if e != nil {
				return input.Chat, e
			}
			out, e := root.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				_ = in.Close()
				return input.Chat, e
			}
			copied = append(copied, p)
			_, copyErr := io.Copy(out, in)
			_ = in.Close() // read-only source
			if copyErr == nil {
				copyErr = out.Sync()
			}
			closeErr := out.Close()
			if e = errors.Join(copyErr, closeErr); e != nil {
				return input.Chat, e
			}
			c.Attachments[i].Path = p
		} else {
			if !validPath(c.ID, a.Path) {
				return input.Chat, errors.New("некорректный путь снимка в архиве")
			}
			// Open through os.Root to reject links/reparse points escaping the archive.
			f, e := root.Open(a.Path)
			if e != nil {
				return input.Chat, e
			}
			_ = f.Close()
		}
	}
	if e := validateDocument(c); e != nil {
		return input.Chat, e
	}
	c.Version = 2
	c.UpdatedAt = chat.Now()
	data, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return input.Chat, e
	}
	if len(data) > maxChatBytes {
		return input.Chat, errors.New("чат превышает лимит истории 32 МиБ")
	}
	if e = storage.Replace(root, "chats/"+c.ID+".json", data); e != nil {
		return input.Chat, fmt.Errorf("не удалось сохранить чат: %w", e)
	}
	committed = true
	return c, nil
}
func (s *Store) Load(id string) (chat.Chat, error) {
	if !idPattern.MatchString(id) {
		return chat.Chat{}, errors.New("некорректный ID чата")
	}
	root, e := os.OpenRoot(s.Dir)
	if e != nil {
		return chat.Chat{}, e
	}
	defer root.Close()
	f, e := root.Open("chats/" + id + ".json")
	if e != nil {
		return chat.Chat{}, e
	}
	defer func() { _ = f.Close() }() // read-only
	data, e := io.ReadAll(io.LimitReader(f, maxChatBytes+1))
	if e != nil {
		return chat.Chat{}, e
	}
	if len(data) > maxChatBytes {
		return chat.Chat{}, errors.New("файл чата слишком большой")
	}
	var version struct {
		Version int `json:"version"`
	}
	if e = json.Unmarshal(data, &version); e != nil {
		return chat.Chat{}, e
	}
	var c chat.Chat
	switch version.Version {
	case 1:
		c, e = readLegacy(data)
	case 2:
		e = json.Unmarshal(data, &c)
	default:
		e = errors.New("неподдерживаемая версия архива")
	}
	if e != nil {
		return chat.Chat{}, e
	}
	if c.ID != id {
		return chat.Chat{}, errors.New("ID чата не совпадает с именем файла")
	}
	for _, a := range c.Attachments {
		if !validPath(id, a.Path) {
			return chat.Chat{}, errors.New("некорректный путь снимка в архиве")
		}
	}
	if e := validateDocument(c); e != nil {
		return chat.Chat{}, e
	}
	// Older archives may carry local offsets; the application works in UTC only.
	c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	// A process can die after a CLI turn was accepted. Never infer success/retry.
	for i, m := range c.Messages {
		for j, answer := range m.Answers {
			if answer.Status == chat.StatusRequesting {
				c.Messages[i].Answers[j].Status = chat.StatusInterrupted
				c.Messages[i].Answers[j].Error = "Приложение завершилось до сохранения результата"
			}
		}
		if m.Status == chat.StatusRequesting {
			c.Messages[i].Status = chat.StatusInterrupted
			for j, a := range m.Attempts {
				if a.Status == chat.StatusRequesting {
					c.Messages[i].Attempts[j].Status = chat.StatusInterrupted
					c.Messages[i].Attempts[j].Error = "Приложение завершилось до сохранения результата"
				}
			}
		}
	}
	return c, nil
}
func (s *Store) List() ([]Summary, error) {
	entries, e := os.ReadDir(filepath.Join(s.Dir, "chats"))
	if e != nil {
		return nil, e
	}
	pins := map[string]bool{}
	if root, e := os.OpenRoot(s.Dir); e == nil {
		// Unreadable pins only lose the grouping; the history itself stays usable.
		pins, _ = s.readPins(root)
		_ = root.Close()
	}
	out := []Summary{}
	for _, f := range entries {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		c, e := s.Load(strings.TrimSuffix(f.Name(), ".json"))
		if e != nil {
			continue
		}
		out = append(out, Summary{c.ID, c.Title, c.ProviderID, c.UpdatedAt, len(c.Messages) == 0, pins[c.ID]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// Exists reports whether a chat document is present, even if it cannot be read.
func (s *Store) Exists(id string) (bool, error) {
	if !idPattern.MatchString(id) {
		return false, nil
	}
	root, e := os.OpenRoot(s.Dir)
	if e != nil {
		return false, e
	}
	defer root.Close()
	_, e = root.Lstat("chats/" + id + ".json")
	if errors.Is(e, os.ErrNotExist) {
		return false, nil
	}
	return e == nil, e
}
func (s *Store) ImagePath(chatID, attachmentID string) (string, error) {
	c, e := s.Load(chatID)
	if e != nil {
		return "", e
	}
	for _, a := range c.Attachments {
		if a.ID == attachmentID {
			return s.AttachmentPath(chatID, a)
		}
	}
	return "", errors.New("снимок не найден")
}

// AttachmentPath resolves an attachment taken from an already loaded chat
// without re-reading the chat document.
func (s *Store) AttachmentPath(chatID string, a chat.Attachment) (string, error) {
	if !idPattern.MatchString(chatID) || !validPath(chatID, a.Path) {
		return "", errors.New("некорректный путь снимка в архиве")
	}
	root, e := os.OpenRoot(s.Dir)
	if e != nil {
		return "", e
	}
	defer root.Close()
	f, e := root.Open(a.Path)
	if e != nil {
		return "", e
	}
	_ = f.Close()
	resolved, e := filepath.EvalSymlinks(filepath.Join(s.Dir, filepath.FromSlash(a.Path)))
	if e != nil {
		return "", e
	}
	// Resolve both sides: Windows short names and archive directory aliases
	// can otherwise make the same directory look like a different root.
	archiveDir, e := filepath.EvalSymlinks(s.Dir)
	if e != nil {
		return "", e
	}
	rel, e := filepath.Rel(archiveDir, resolved)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("снимок указывает за пределы архива")
	}
	return resolved, nil
}

// Delete removes a chat with its images. Images are moved to trash first, so a
// failure can never leave them without a chat document: if the document cannot
// be removed they are moved back, and sweepTrash settles an interrupted attempt.
func (s *Store) Delete(id string) error {
	if !idPattern.MatchString(id) {
		return errors.New("некорректный ID чата")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, e := os.OpenRoot(s.Dir)
	if e != nil {
		return e
	}
	defer root.Close()
	if _, e = root.Lstat("chats/" + id + ".json"); e != nil {
		return e
	}
	trash := ""
	if _, e = root.Lstat("images/" + id); e == nil {
		if e = root.MkdirAll("trash", 0700); e != nil {
			return e
		}
		trash = "trash/" + id + "." + rand.Text()
		if e = root.Rename("images/"+id, trash); e != nil {
			return fmt.Errorf("не удалось удалить снимки чата: %w", e)
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if e = root.Remove("chats/" + id + ".json"); e != nil {
		if trash != "" {
			e = errors.Join(e, root.Rename(trash, "images/"+id))
		}
		return e
	}
	// The chat is gone; leftovers below are only disk space for the next sweep.
	if trash != "" {
		_ = root.RemoveAll(trash)
	}
	if pins, e := s.readPins(root); e == nil && pins[id] {
		delete(pins, id)
		_ = s.writePins(root, pins)
	}
	return nil
}
