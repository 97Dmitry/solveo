package history

import (
	"encoding/json"
	"errors"
	"os"
	"sort"

	"github.com/97Dmitry/solveo/internal/storage"
)

const pinsFile = "pins.json"

type pinsDocument struct {
	Version int      `json:"version"`
	ChatIDs []string `json:"chat_ids"`
}

// errFuturePins protects a pins file written by a newer Solveo from being replaced.
var errFuturePins = errors.New("файл закреплённых чатов создан более новой версией Solveo")

// readPins reads pinned chat IDs; writers hold s.mu. A missing or malformed file is an
// empty set: pins are disposable metadata and the next change rewrites them.
func (s *Store) readPins(root *os.Root) (map[string]bool, error) {
	pins := map[string]bool{}
	data, e := root.ReadFile(pinsFile)
	if errors.Is(e, os.ErrNotExist) {
		return pins, nil
	}
	if e != nil {
		return pins, e
	}
	var doc pinsDocument
	if json.Unmarshal(data, &doc) != nil {
		return pins, nil
	}
	if doc.Version > 1 {
		return pins, errFuturePins
	}
	if doc.Version != 1 {
		return pins, nil
	}
	for _, id := range doc.ChatIDs {
		if idPattern.MatchString(id) {
			pins[id] = true
		}
	}
	return pins, nil
}
func (s *Store) writePins(root *os.Root, pins map[string]bool) error {
	doc := pinsDocument{Version: 1, ChatIDs: []string{}}
	for id := range pins {
		doc.ChatIDs = append(doc.ChatIDs, id)
	}
	sort.Strings(doc.ChatIDs)
	data, e := json.MarshalIndent(doc, "", "  ")
	if e != nil {
		return e
	}
	return storage.Replace(root, pinsFile, data)
}

// SetPinned pins or unpins an existing chat without touching its document.
func (s *Store) SetPinned(id string, pinned bool) error {
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
	if pinned {
		if _, e = root.Lstat("chats/" + id + ".json"); e != nil {
			return errors.New("чат не найден")
		}
	}
	pins, e := s.readPins(root)
	if e != nil {
		return e
	}
	if pins[id] == pinned {
		return nil
	}
	if pinned {
		pins[id] = true
	} else {
		delete(pins, id)
	}
	return s.writePins(root, pins)
}
