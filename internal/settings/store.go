package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/hotkeys"
	"github.com/97Dmitry/solveo/internal/storage"
)

type Config struct {
	GlobalInstructions string `json:"global_instructions"`
	UIScale            int    `json:"ui_scale"`
	Theme              string `json:"theme"`
	FontSize           int    `json:"font_size"`
	AlwaysOnTop        bool   `json:"always_on_top"`
	// Hotkeys maps every hotkeys.Actions entry to canonical text; "" disables it.
	Hotkeys map[string]string `json:"hotkeys"`
	Audio   Audio             `json:"audio"`
}

// Audio holds speech recognition preferences; ContextPhrases is the term dictionary sent at listening start.
type Audio struct {
	// Provider is the selected recognizer ID; "" or an unknown ID means the application's default.
	Provider       string   `json:"provider"`
	ContextPhrases []string `json:"context_phrases"`
}

// Dictionary limits; the worker additionally caps the full context at 4 KiB including its template.
const (
	MaxContextPhrases      = 256
	MaxContextPhraseRunes  = 128
	MaxContextPhrasesBytes = 3800
)

func validProviderID(id string) bool {
	return len(id) <= 64 && strings.Trim(id, "abcdefghijklmnopqrstuvwxyz0123456789-") == ""
}

func validateAudio(a Audio) error {
	if !validProviderID(a.Provider) {
		return errors.New("некорректный идентификатор распознавателя")
	}
	if len(a.ContextPhrases) > MaxContextPhrases {
		return fmt.Errorf("в словаре распознавания больше %d терминов", MaxContextPhrases)
	}
	total := 0
	for _, p := range a.ContextPhrases {
		if !utf8.ValidString(p) || strings.ContainsRune(p, 0) || strings.TrimSpace(p) == "" {
			return errors.New("термин словаря распознавания пустой или содержит недопустимые символы")
		}
		if utf8.RuneCountInString(p) > MaxContextPhraseRunes {
			return fmt.Errorf("термин словаря распознавания длиннее %d символов", MaxContextPhraseRunes)
		}
		total += len(p) + 1
	}
	if total > MaxContextPhrasesBytes {
		return fmt.Errorf("словарь распознавания больше %d байт", MaxContextPhrasesBytes)
	}
	return nil
}

func normalizeAudio(a Audio) Audio {
	out := []string{}
	seen := map[string]bool{}
	for _, p := range a.ContextPhrases {
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	provider := strings.TrimSpace(a.Provider)
	if !validProviderID(provider) {
		provider = "" // a hand-edited or future ID must not block startup; the default provider applies
	}
	return Audio{Provider: provider, ContextPhrases: out}
}

type Store struct{ Dir string }
type storedSettings struct {
	Config
	SchemaVersion int `json:"schema_version"`
}

func Open(dir string) *Store { return &Store{Dir: dir} }
func Validate(c Config) error {
	if !chat.ValidGlobalInstructions(c.GlobalInstructions) {
		return errors.New("общие инструкции длиннее 16384 символов или содержат некорректный UTF-8")
	}
	if c.UIScale < 80 || c.UIScale > 150 {
		return errors.New("масштаб интерфейса должен быть от 80 до 150%")
	}
	if c.Theme != "light" && c.Theme != "dark" {
		return errors.New("неизвестная тема")
	}
	if c.FontSize < 14 || c.FontSize > 30 {
		return errors.New("размер текста должен быть от 14 до 30")
	}
	normal, e := hotkeys.Normalize(c.Hotkeys)
	if e != nil {
		return e
	}
	if !maps.Equal(normal, c.Hotkeys) {
		return errors.New("горячие клавиши не приведены к стандартной записи")
	}
	return validateAudio(c.Audio)
}

// Normalize brings user-entered settings to the stored form before Validate.
func Normalize(c Config) (Config, error) {
	normal, e := hotkeys.Normalize(c.Hotkeys)
	if e != nil {
		return c, e
	}
	c.Hotkeys = normal
	c.Audio = normalizeAudio(c.Audio)
	return c, nil
}
func (s *Store) Load() (Config, error) {
	c := Config{Theme: "dark", FontSize: 17, UIScale: 100, Hotkeys: hotkeys.Defaults(), Audio: Audio{ContextPhrases: []string{}}}
	root, e := os.OpenRoot(s.Dir)
	if e != nil {
		return c, e
	}
	defer root.Close()
	b, e := root.ReadFile("settings.json")
	if errors.Is(e, os.ErrNotExist) {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	disk := storedSettings{Config: Config{Theme: "dark", FontSize: 15, UIScale: 100}}
	if e = json.Unmarshal(b, &disk); e != nil {
		return c, e
	}
	switch disk.SchemaVersion {
	case 0:
		if disk.FontSize < 12 || disk.FontSize > 28 {
			return c, errors.New("некорректный размер текста в старых настройках")
		}
		disk.FontSize += 2
	case 1:
	default:
		return c, errors.New("неподдерживаемая версия файла настроек")
	}
	// Files without hotkeys get the defaults; actions of a newer version are ignored.
	stored := disk.Hotkeys
	disk.Hotkeys = hotkeys.Defaults()
	for action := range disk.Hotkeys {
		if v, ok := stored[action]; ok {
			disk.Hotkeys[action] = v
		}
	}
	c, e = Normalize(disk.Config)
	if e != nil {
		return c, e
	}
	return c, Validate(c)
}
func (s *Store) Save(c Config) error {
	if e := Validate(c); e != nil {
		return e
	}
	root, e := os.OpenRoot(s.Dir)
	if e != nil {
		return e
	}
	defer root.Close()
	b, e := json.MarshalIndent(storedSettings{Config: c, SchemaVersion: 1}, "", "  ")
	if e != nil {
		return e
	}
	return storage.Replace(root, "settings.json", b)
}
