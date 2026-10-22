// Package profiles stores reusable preparation, independently of chats and providers.
package profiles

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/storage"
)

const (
	MaxProfiles      = 100
	MaxDocumentBytes = 32 << 20
	maxRevision      = 1<<53 - 1
)

type Profile struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Config    chat.Config `json:"config"`
	Revision  uint64      `json:"revision"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}
type document struct {
	Version  int       `json:"version"`
	Profiles []Profile `json:"profiles"`
}
type Store struct {
	dir      string
	mu       sync.Mutex
	writable bool
}

func New(dir string) *Store { return &Store{dir: dir} }

func Validate(p Profile) error {
	id, err := hex.DecodeString(p.ID)
	if err != nil || len(id) != 16 || strings.ToLower(p.ID) != p.ID || p.Revision == 0 || p.Revision > maxRevision || p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() || p.UpdatedAt.Before(p.CreatedAt) {
		return errors.New("некорректные метаданные профиля")
	}
	if p.Name != strings.TrimSpace(p.Name) || p.Name == "" || utf8.RuneCountInString(p.Name) > 160 || !utf8.ValidString(p.Name) || strings.ContainsRune(p.Name, 0) {
		return errors.New("название профиля должно содержать от 1 до 160 символов без NUL")
	}
	return chat.ValidateConfig(p.Config)
}
func validateList(list []Profile) error {
	if len(list) > MaxProfiles {
		return errors.New("можно сохранить не более 100 профилей")
	}
	seen := map[string]bool{}
	for _, p := range list {
		if err := Validate(p); err != nil {
			return err
		}
		if seen[p.ID] {
			return errors.New("повторяющийся ID профиля")
		}
		seen[p.ID] = true
	}
	return nil
}
func (s *Store) Load() ([]Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writable = false
	list, err := s.load()
	if err == nil {
		s.writable = true
	}
	return list, err
}
func (s *Store) load() ([]Profile, error) {
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, errors.New("не удалось открыть каталог профилей")
	}
	defer root.Close()
	info, err := root.Stat("profiles.json")
	if errors.Is(err, os.ErrNotExist) {
		return []Profile{}, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("не удалось прочитать файл профилей")
	}
	f, err := root.Open("profiles.json")
	if err != nil {
		return nil, errors.New("не удалось прочитать файл профилей")
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, MaxDocumentBytes+1))
	if err != nil || len(b) > MaxDocumentBytes || !utf8.Valid(b) {
		return nil, errors.New("файл профилей повреждён или превышает 32 МиБ")
	}
	var doc document
	if json.Unmarshal(b, &doc) != nil || doc.Version != 1 || doc.Profiles == nil {
		return nil, errors.New("файл профилей повреждён или имеет неподдерживаемую версию")
	}
	if err = validateList(doc.Profiles); err != nil {
		return nil, err
	}
	for i := range doc.Profiles {
		doc.Profiles[i].CreatedAt = doc.Profiles[i].CreatedAt.UTC()
		doc.Profiles[i].UpdatedAt = doc.Profiles[i].UpdatedAt.UTC()
	}
	return doc.Profiles, nil
}
func (s *Store) Save(list []Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.writable {
		return errors.New("сначала успешно перечитайте библиотеку профилей")
	}
	if err := validateList(list); err != nil {
		return err
	}
	list = append([]Profile{}, list...)
	for i := range list {
		list[i].CreatedAt = list[i].CreatedAt.UTC()
		list[i].UpdatedAt = list[i].UpdatedAt.UTC()
	}
	b, err := json.MarshalIndent(document{Version: 1, Profiles: list}, "", "  ")
	if err != nil || len(b) > MaxDocumentBytes {
		return errors.New("библиотека профилей превышает 32 МиБ")
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return errors.New("не удалось открыть каталог профилей")
	}
	defer root.Close()
	if err = storage.Replace(root, "profiles.json", b); err != nil {
		return errors.New("не удалось сохранить библиотеку профилей")
	}
	return nil
}
