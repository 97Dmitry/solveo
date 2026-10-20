package settings

import (
	"encoding/json"
	"errors"
	"github.com/97Dmitry/solveo/internal/storage"
	"os"
)

type Config struct {
	UIScale     int    `json:"ui_scale"`
	Theme       string `json:"theme"`
	FontSize    int    `json:"font_size"`
	AlwaysOnTop bool   `json:"always_on_top"`
}
type Store struct{ Dir string }
type storedSettings struct {
	Config
	SchemaVersion int `json:"schema_version"`
}

func Open(dir string) *Store { return &Store{Dir: dir} }
func Validate(c Config) error {
	if c.UIScale < 80 || c.UIScale > 150 {
		return errors.New("UI scale must be 80..150 percent")
	}
	if c.Theme != "light" && c.Theme != "dark" {
		return errors.New("invalid theme")
	}
	if c.FontSize < 14 || c.FontSize > 30 {
		return errors.New("font size must be 14..30")
	}
	return nil
}
func (s *Store) Load() (Config, error) {
	c := Config{Theme: "dark", FontSize: 17, UIScale: 100}
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
			return c, errors.New("invalid legacy font size")
		}
		disk.FontSize += 2
	case 1:
	default:
		return c, errors.New("unsupported settings schema version")
	}
	c = disk.Config
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
