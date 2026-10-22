// Package workstate persists unfinished work beside the chat archive: drafts,
// queued PNGs and checkpoints of answers that are still arriving. Completed chat
// documents stay in internal/history.
package workstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/storage"
)

const (
	version        = 1
	maxStateBytes  = 8 << 20
	maxStreamBytes = 16 << 20
	maxDraftBytes  = 1 << 20
	MaxLabelBytes  = 200
	// MaxQueue mirrors the per-chat image limit of the controller.
	MaxQueue = 20
	// MaxBlocks bounds stored answer checkpoints (two blocks of a few interrupted turns).
	MaxBlocks = 8

	stateFile  = "state.json"
	streamFile = "stream.json"
	queueDir   = "queue"
	pngSuffix  = ".png"
	tmpSuffix  = ".png.tmp"
)

// ErrUnsupported means the file was written by a newer Solveo and must not be overwritten.
var ErrUnsupported = errors.New("рабочее состояние создано более новой версией Solveo; черновики и очередь не сохраняются")

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)

type Draft struct {
	ReplyToID   string `json:"reply_to_id,omitempty"`
	ReplyToKind string `json:"reply_to_kind,omitempty"`
	Text        string `json:"text"`
	Revision    uint64 `json:"revision"`
}

// Item is a queued PNG stored as queue/<ID>.png; order is the send order.
type Item struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Submitted is the draft revision acknowledged by a question whose outcome is
// not archived yet. After a crash it tells whether the draft only repeats it.
type Submitted struct {
	MessageID string `json:"message_id"`
	Revision  uint64 `json:"revision"`
}

type Chat struct {
	Draft     Draft      `json:"draft"`
	Queue     []Item     `json:"queue"`
	Submitted *Submitted `json:"submitted,omitempty"`
}

// State holds only chats with unfinished work, keyed by chat ID.
type State struct {
	Chats map[string]Chat
}

// Block is the last received text of an answer block. It is never a completed answer.
type Block struct {
	ChatID    string    `json:"chat_id"`
	MessageID string    `json:"message_id"`
	AttemptID string    `json:"attempt_id"`
	Block     string    `json:"block"`
	Text      string    `json:"text"`
	UpdatedAt time.Time `json:"updated_at"`
}

type stateDocument struct {
	Version int             `json:"version"`
	Chats   map[string]Chat `json:"chats"`
}
type streamDocument struct {
	Version int     `json:"version"`
	Blocks  []Block `json:"blocks"`
}

type Store struct{ dir string }

func Open(dir string) (*Store, error) {
	dir, e := filepath.Abs(dir)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(filepath.Join(dir, queueDir), 0700); e != nil {
		return nil, e
	}
	return &Store{dir: dir}, nil
}

// ValidID reports whether id can name a chat, message or queued image.
func ValidID(id string) bool { return idPattern.MatchString(id) }

func validateState(chats map[string]Chat) error {
	seen := map[string]bool{}
	for id, c := range chats {
		if !ValidID(id) {
			return errors.New("некорректный ID чата")
		}
		if (c.Draft.ReplyToID != "" && !ValidID(c.Draft.ReplyToID)) || (c.Draft.ReplyToID == "" && c.Draft.ReplyToKind != "") || (c.Draft.ReplyToKind != "" && c.Draft.ReplyToKind != chat.AnswerApproach && c.Draft.ReplyToKind != chat.AnswerSolution) {
			return errors.New("некорректная ссылка черновика")
		}
		if len(c.Draft.Text) > maxDraftBytes {
			return errors.New("черновик длиннее 1 МиБ")
		}
		if len(c.Queue) > MaxQueue {
			return errors.New("очередь длиннее 20 снимков")
		}
		for _, item := range c.Queue {
			if !ValidID(item.ID) || seen[item.ID] || len(item.Label) > MaxLabelBytes {
				return errors.New("некорректный снимок очереди")
			}
			seen[item.ID] = true
		}
		if c.Submitted != nil && !ValidID(c.Submitted.MessageID) {
			return errors.New("некорректная отметка отправленного вопроса")
		}
	}
	return nil
}

func validateBlocks(blocks []Block) error {
	if len(blocks) > MaxBlocks {
		return errors.New("слишком много сохранённых частей ответа")
	}
	for _, b := range blocks {
		if !ValidID(b.ChatID) || !ValidID(b.MessageID) || !ValidID(b.AttemptID) {
			return errors.New("некорректная часть ответа")
		}
		switch b.Block {
		case "message", "approach", "solution":
		default:
			return errors.New("некорректный блок ответа")
		}
	}
	return nil
}

func readLimited(root *os.Root, name string, limit int64) ([]byte, error) {
	f, e := root.Open(name)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }() // read-only
	data, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(data)) > limit {
		return nil, errBadDocument{errors.New("файл слишком большой")}
	}
	return data, nil
}

// errBadDocument marks content that can be set aside instead of an I/O failure.
type errBadDocument struct{ error }

// Load returns the saved work. A malformed file is moved aside to state.json.bad-<UTC>
// and reported in notice. An error (unreadable or newer file) means the caller must
// not save over it.
func (s *Store) Load() (State, string, error) {
	empty := State{Chats: map[string]Chat{}}
	root, e := os.OpenRoot(s.dir)
	if e != nil {
		return State{}, "", e
	}
	defer root.Close()
	data, e := readLimited(root, stateFile, maxStateBytes)
	if errors.Is(e, os.ErrNotExist) {
		return empty, "", nil
	}
	var bad errBadDocument
	if e != nil && !errors.As(e, &bad) {
		return State{}, "", e
	}
	if e == nil {
		// Inspect the envelope before decoding fields that future versions may change.
		var envelope struct {
			Version int `json:"version"`
		}
		if e = json.Unmarshal(data, &envelope); e == nil && envelope.Version > version {
			return State{}, "", ErrUnsupported
		}
		var doc stateDocument
		if e == nil {
			e = json.Unmarshal(data, &doc)
		}
		if e == nil {
			switch {
			case doc.Version > version:
				return State{}, "", ErrUnsupported
			case doc.Version != version:
				e = errors.New("неизвестная версия")
			default:
				if e = validateState(doc.Chats); e == nil {
					if doc.Chats == nil {
						doc.Chats = map[string]Chat{}
					}
					return State{Chats: doc.Chats}, "", nil
				}
			}
		}
	}
	aside := stateFile + ".bad-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if err := root.Rename(stateFile, aside); err != nil {
		return State{}, "", fmt.Errorf("повреждённое рабочее состояние не удалось отложить: %w", err)
	}
	return empty, fmt.Sprintf("Черновики и очередь не восстановлены: файл повреждён (%v) и сохранён как work/%s", e, aside), nil
}

func (s *Store) Save(st State) error {
	chats := st.Chats
	if chats == nil {
		chats = map[string]Chat{}
	}
	if e := validateState(chats); e != nil {
		return e
	}
	return s.write(stateFile, stateDocument{Version: version, Chats: chats}, maxStateBytes)
}

// LoadStream returns stored answer checkpoints; a missing file is empty.
func (s *Store) LoadStream() ([]Block, error) {
	root, e := os.OpenRoot(s.dir)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	data, e := readLimited(root, streamFile, maxStreamBytes)
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var doc streamDocument
	if e = json.Unmarshal(data, &doc); e != nil {
		return nil, e
	}
	if doc.Version != version {
		return nil, errors.New("неподдерживаемая версия сохранённых частей ответа")
	}
	if e = validateBlocks(doc.Blocks); e != nil {
		return nil, e
	}
	for i := range doc.Blocks {
		doc.Blocks[i].UpdatedAt = doc.Blocks[i].UpdatedAt.UTC()
	}
	return doc.Blocks, nil
}

func (s *Store) SaveStream(blocks []Block) error {
	if blocks == nil {
		blocks = []Block{}
	}
	if e := validateBlocks(blocks); e != nil {
		return e
	}
	return s.write(streamFile, streamDocument{Version: version, Blocks: blocks}, maxStreamBytes)
}

func (s *Store) write(name string, doc any, limit int) error {
	data, e := json.Marshal(doc)
	if e != nil {
		return e
	}
	if len(data) > limit {
		return errors.New("рабочее состояние превышает допустимый размер")
	}
	root, e := os.OpenRoot(s.dir)
	if e != nil {
		return e
	}
	defer root.Close()
	return storage.Replace(root, name, data)
}

// CapturePath is where a capturer writes a new PNG before Commit.
func (s *Store) CapturePath(id string) (string, error) {
	if !ValidID(id) {
		return "", errors.New("некорректный ID снимка")
	}
	root, e := s.queueRoot()
	if e != nil {
		return "", e
	}
	defer root.Close()
	return filepath.Join(s.dir, queueDir, id+tmpSuffix), nil
}

// Path is the location of a committed queued PNG.
func (s *Store) Path(id string) (string, error) {
	if !ValidID(id) {
		return "", errors.New("некорректный ID снимка")
	}
	root, e := s.queueRoot()
	if e != nil {
		return "", e
	}
	defer root.Close()
	return filepath.Join(s.dir, queueDir, id+pngSuffix), nil
}

// Commit flushes a captured PNG to disk and gives it its final name, so a crash
// never leaves a partially written image under a queued name.
func (s *Store) Commit(id string) (string, error) {
	final, e := s.Path(id)
	if e != nil {
		return "", e
	}
	root, e := s.queueRoot()
	if e != nil {
		return "", e
	}
	defer root.Close()
	f, e := root.OpenFile(queueDir+"/"+id+tmpSuffix, os.O_RDWR, 0)
	if e != nil {
		return "", e
	}
	syncErr := f.Sync()
	if e = errors.Join(syncErr, f.Close()); e != nil {
		return "", e
	}
	if e = root.Rename(queueDir+"/"+id+tmpSuffix, queueDir+"/"+id+pngSuffix); e != nil {
		return "", e
	}
	return final, nil
}

// queueRoot confines every queue operation to work, rejecting a replaced queue.
func (s *Store) queueRoot() (*os.Root, error) {
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, err
	}
	info, err := root.Lstat(queueDir)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		_ = root.Close()
		return nil, errors.New("папка очереди должна быть обычным каталогом внутри work")
	}
	return root, nil
}

// Has distinguishes an absent PNG from an inaccessible queue. Errors must never
// cause its entry to be forgotten or its source to be pruned.
func (s *Store) Has(id string) (bool, error) {
	if !ValidID(id) {
		return false, errors.New("некорректный ID снимка")
	}
	root, err := s.queueRoot()
	if err != nil {
		return false, err
	}
	defer root.Close()
	info, err := root.Lstat(queueDir + "/" + id + pngSuffix)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("снимок очереди не является обычным файлом")
	}
	return true, nil
}

// Prune removes unreferenced entries without following links outside work.
func (s *Store) Prune(keep map[string]bool) error {
	root, err := s.queueRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	queue, err := root.Open(queueDir)
	if err != nil {
		return err
	}
	entries, err := queue.ReadDir(-1)
	err = errors.Join(err, queue.Close())
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, pngSuffix) && keep[strings.TrimSuffix(name, pngSuffix)] && entry.Type().IsRegular() {
			continue
		}
		errs = append(errs, root.RemoveAll(queueDir+"/"+name))
	}
	dir, err := root.Open(".")
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	entries, err = dir.ReadDir(-1)
	err = errors.Join(err, dir.Close())
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".solveo-") {
			errs = append(errs, root.Remove(entry.Name()))
		}
	}
	return errors.Join(errs...)
}

// WriteImage installs verified PNG bytes as a new independently owned queue file.
func (s *Store) WriteImage(id string, data []byte) (string, error) {
	if !ValidID(id) {
		return "", errors.New("некорректный ID снимка")
	}
	root, err := s.queueRoot()
	if err != nil {
		return "", err
	}
	defer root.Close()
	name := queueDir + "/" + id + tmpSuffix
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	committed := false
	defer func() {
		if !committed {
			_ = root.Remove(name)
		}
	}()
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	if err = errors.Join(writeErr, syncErr, f.Close()); err != nil {
		return "", err
	}
	p, err := s.Commit(id)
	if err != nil {
		return "", err
	}
	committed = true
	return p, nil
}
