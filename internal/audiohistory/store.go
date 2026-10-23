// Package audiohistory persists text-only ASR sessions independently of chats.
package audiohistory

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/97Dmitry/solveo/internal/storage"
)

const (
	MaxSegments      = 10000
	MaxDocumentBytes = 32 << 20
	maxHeaderBytes   = 64 << 10
)

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var directoryLocks sync.Map

type Segment struct {
	ID        string    `json:"id"`
	StreamID  string    `json:"stream_id"`
	Track     string    `json:"track"`
	Text      string    `json:"text"`
	StartedAt time.Time `json:"started_at"`
	FinalAt   time.Time `json:"final_at"`
}
type Source struct {
	StreamID string `json:"stream_id"`
	Track    string `json:"track"`
	Device   string `json:"device"`
}
type Failure struct {
	At      time.Time `json:"at"`
	Track   string    `json:"track"`
	Message string    `json:"message"`
}
type Submission struct {
	At          time.Time `json:"at"`
	ChatID      string    `json:"chat_id"`
	OperationID string    `json:"operation_id"`
	SegmentIDs  []string  `json:"segment_ids"`
}
type Session struct {
	ChatID        string       `json:"chat_id,omitempty"`
	ID            string       `json:"id"`
	ProviderID    string       `json:"provider_id"`
	ProviderLabel string       `json:"provider_label"`
	StartedAt     time.Time    `json:"started_at"`
	EndedAt       time.Time    `json:"ended_at"`
	Dictionary    []string     `json:"dictionary"`
	Sources       []Source     `json:"sources"`
	Segments      []Segment    `json:"segments"`
	Errors        []Failure    `json:"errors"`
	Submissions   []Submission `json:"submissions"`
}
type Summary struct {
	ChatID        string    `json:"chat_id,omitempty"`
	ID            string    `json:"id"`
	ProviderID    string    `json:"provider_id"`
	ProviderLabel string    `json:"provider_label"`
	StartedAt     time.Time `json:"started_at"`
	EndedAt       time.Time `json:"ended_at"`
	SegmentCount  int       `json:"segment_count"`
}
type document struct {
	Version int     `json:"version"`
	Summary Summary `json:"summary"`
	Session Session `json:"session"`
}
type Store struct {
	dir string
	mu  *sync.Mutex
}

func New(dir string) *Store {
	if absolute, err := filepath.Abs(dir); err == nil {
		dir = absolute
	}
	lock, _ := directoryLocks.LoadOrStore(filepath.Clean(dir), &sync.Mutex{})
	return &Store{dir: dir, mu: lock.(*sync.Mutex)}
}
func Clone(s Session) Session {
	s.Dictionary = slices.Clone(s.Dictionary)
	s.Sources = slices.Clone(s.Sources)
	s.Segments = slices.Clone(s.Segments)
	s.Errors = slices.Clone(s.Errors)
	s.Submissions = slices.Clone(s.Submissions)
	for i := range s.Submissions {
		s.Submissions[i].SegmentIDs = slices.Clone(s.Submissions[i].SegmentIDs)
	}
	return s
}
func failure(message string) error { return errors.New("История аудио: " + message) }
func (s *Store) Save(session Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session = Clone(session)
	normalize(&session)
	data, err := encodeDocument(session)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(s.dir, 0700); err != nil {
		return failure("не удалось создать хранилище")
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return failure("не удалось открыть хранилище")
	}
	defer root.Close()
	old, err := load(root, session.ID)
	if err == nil {
		if old.StartedAt != session.StartedAt || old.ProviderID != session.ProviderID || old.ChatID != session.ChatID {
			return failure("идентификатор, чат, время начала и провайдер сеанса нельзя изменить")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return failure("существующий документ нельзя перезаписать")
	}
	if err = storage.Replace(root, session.ID+".json", data); err != nil {
		return failure("не удалось сохранить сеанс")
	}
	return nil
}
func (s *Store) Load(id string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !idPattern.MatchString(id) {
		return Session{}, failure("некорректный идентификатор сеанса")
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return Session{}, failure("не удалось открыть хранилище")
	}
	defer root.Close()
	session, err := load(root, id)
	if err != nil {
		return Session{}, failure("не удалось прочитать сеанс")
	}
	return session, nil
}
func load(root *os.Root, id string) (Session, error) {
	f, err := root.Open(id + ".json")
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxDocumentBytes {
		return Session{}, failure("недопустимый размер документа")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxDocumentBytes+1))
	if err != nil || len(data) > MaxDocumentBytes || !utf8.Valid(data) {
		return Session{}, failure("документ повреждён")
	}
	var doc document
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&doc); err != nil {
		return Session{}, failure("документ повреждён")
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return Session{}, failure("документ повреждён")
	}
	normalize(&doc.Session)
	normalizeSummary(&doc.Summary)
	if (doc.Version != 1 && doc.Version != 2) || doc.Session.ID != id {
		return Session{}, failure("документ повреждён или имеет неподдерживаемую версию")
	}
	if err = Validate(doc.Session); err != nil {
		return Session{}, err
	}
	if doc.Summary != summarize(doc.Session) {
		return Session{}, failure("краткие сведения не соответствуют содержимому сеанса")
	}
	return doc.Session, nil
}
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !idPattern.MatchString(id) {
		return failure("некорректный идентификатор сеанса")
	}
	root, err := os.OpenRoot(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return failure("не удалось открыть хранилище")
	}
	defer root.Close()
	if err = root.Remove(id + ".json"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return failure("не удалось удалить сеанс")
	}
	return nil
}
func (s *Store) List() ([]Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []Summary{}
	root, err := os.OpenRoot(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, failure("не удалось открыть хранилище")
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return result, failure("не удалось прочитать список сеансов")
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return result, failure("не удалось прочитать список сеансов")
	}
	type candidate struct {
		name  string
		mtime time.Time
	}
	candidates := make([]candidate, 0, len(entries))
	invalid := false
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || !idPattern.MatchString(strings.TrimSuffix(name, ".json")) {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			invalid = true
			continue
		}
		if !info.Mode().IsRegular() {
			invalid = true
			continue
		}
		candidates = append(candidates, candidate{name, info.ModTime()})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].mtime.Equal(candidates[j].mtime) {
			return candidates[i].name < candidates[j].name
		}
		return candidates[i].mtime.After(candidates[j].mtime)
	})
	if len(candidates) > 200 {
		candidates = candidates[:200]
	}
	for _, candidate := range candidates {
		sum, e := readSummary(root, candidate.name)
		if e != nil {
			invalid = true
			continue
		}
		result = append(result, sum)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].StartedAt.Equal(result[j].StartedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].StartedAt.After(result[j].StartedAt)
	})
	if invalid {
		return result, failure("не удалось прочитать заголовки некоторых сеансов")
	}
	return result, nil
}
func readSummary(root *os.Root, name string) (Summary, error) {
	f, err := root.Open(name)
	if err != nil {
		return Summary{}, failure("не удалось открыть заголовок")
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxDocumentBytes {
		return Summary{}, failure("недопустимый размер документа")
	}
	decoder := json.NewDecoder(io.LimitReader(f, maxHeaderBytes))
	decoder.DisallowUnknownFields()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return Summary{}, failure("заголовок повреждён")
	}
	key, err := decoder.Token()
	if err != nil || key != "version" {
		return Summary{}, failure("заголовок повреждён")
	}
	var version int
	if decoder.Decode(&version) != nil || (version != 1 && version != 2) {
		return Summary{}, failure("версия документа не поддерживается")
	}
	key, err = decoder.Token()
	if err != nil || key != "summary" {
		return Summary{}, failure("заголовок повреждён")
	}
	var sum Summary
	if decoder.Decode(&sum) != nil {
		return Summary{}, failure("краткие сведения о сеансе повреждены")
	}
	normalizeSummary(&sum)
	if sum.ID+".json" != name || !validSummary(sum) {
		return Summary{}, failure("краткие сведения о сеансе повреждены")
	}
	return sum, nil
}
func summarize(s Session) Summary {
	return Summary{ChatID: s.ChatID, ID: s.ID, ProviderID: s.ProviderID, ProviderLabel: s.ProviderLabel, StartedAt: s.StartedAt, EndedAt: s.EndedAt, SegmentCount: len(s.Segments)}
}
func normalizeSummary(s *Summary) { s.StartedAt = s.StartedAt.UTC(); s.EndedAt = s.EndedAt.UTC() }
func normalize(s *Session) {
	s.StartedAt = s.StartedAt.UTC()
	s.EndedAt = s.EndedAt.UTC()
	for i := range s.Segments {
		s.Segments[i].StartedAt = s.Segments[i].StartedAt.UTC()
		s.Segments[i].FinalAt = s.Segments[i].FinalAt.UTC()
	}
	for i := range s.Errors {
		s.Errors[i].At = s.Errors[i].At.UTC()
	}
	for i := range s.Submissions {
		s.Submissions[i].At = s.Submissions[i].At.UTC()
	}
}
func validText(s string, max int, required bool) bool {
	return len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0) && (!required || strings.TrimSpace(s) != "")
}
func validTrack(track string) bool { return track == "system" || track == "mic" }
func validSummary(s Summary) bool {
	return (s.ChatID == "" || idPattern.MatchString(s.ChatID)) && idPattern.MatchString(s.ID) && validText(s.ProviderID, 256, true) && validText(s.ProviderLabel, 512, true) && !s.StartedAt.IsZero() && (s.EndedAt.IsZero() || !s.EndedAt.Before(s.StartedAt)) && s.SegmentCount >= 0 && s.SegmentCount <= MaxSegments
}

// Validate checks whether a text-only session can be persisted.
func Validate(s Session) error {
	if !validSummary(summarize(s)) || len(s.Dictionary) > 256 || len(s.Sources) > 2 || len(s.Errors) > 500 || len(s.Submissions) > 1000 {
		return failure("некорректные сведения о сеансе или превышены допустимые ограничения")
	}
	for _, word := range s.Dictionary {
		if !validText(word, 1024, true) {
			return failure("некорректный словарь")
		}
	}
	for _, source := range s.Sources {
		if !validText(source.StreamID, 256, true) || !validTrack(source.Track) || !validText(source.Device, 4096, false) {
			return failure("некорректный источник звука")
		}
	}
	textBytes := 0
	ids := make(map[string]struct{}, len(s.Segments))
	for _, segment := range s.Segments {
		textBytes += len(segment.Text)
		if textBytes > MaxDocumentBytes {
			return failure("документ превышает допустимый размер")
		}
		if err := ValidateSegment(segment); err != nil {
			return err
		}
		if _, exists := ids[segment.ID]; exists {
			return failure("повторяется идентификатор реплики")
		}
		ids[segment.ID] = struct{}{}
	}
	for _, item := range s.Errors {
		if item.At.IsZero() || item.At.Before(s.StartedAt) || (item.Track != "" && !validTrack(item.Track)) || !validText(item.Message, 16384, true) {
			return failure("некорректные сведения об ошибке сеанса")
		}
	}
	for _, item := range s.Submissions {
		if item.At.IsZero() || item.At.Before(s.StartedAt) || !validText(item.ChatID, 256, true) || !validText(item.OperationID, 256, true) || len(item.SegmentIDs) == 0 || len(item.SegmentIDs) > MaxSegments {
			return failure("некорректные сведения об отправке в чат")
		}
		seen := make(map[string]struct{}, len(item.SegmentIDs))
		for _, id := range item.SegmentIDs {
			if _, exists := ids[id]; !exists {
				return failure("отправка ссылается на неизвестную реплику")
			}
			if _, exists := seen[id]; exists {
				return failure("реплика повторяется в одной отправке")
			}
			seen[id] = struct{}{}
		}
	}
	return validateDocumentSize(s)
}

// ValidateSegment checks an individual final row independently of session metadata.
// StartedAt can precede the session by a fractional second because UI timestamps
// use RFC3339 seconds; FinalAt is the application observation of the final event.
func ValidateSegment(segment Segment) error {
	if !validText(segment.ID, 256, true) || !validText(segment.StreamID, 256, true) || !validTrack(segment.Track) || !validText(segment.Text, 256<<10, true) || utf8.RuneCountInString(segment.Text) > 65536 || segment.StartedAt.IsZero() || segment.FinalAt.Before(segment.StartedAt) {
		return failure("некорректная окончательная реплика")
	}
	return nil
}

// Count arrays individually so validation never marshals an unbounded session.
// The skeleton already includes each null; replace those four-byte values with
// the exact encoded array sizes before Save allocates the complete document.
func validateDocumentSize(s Session) error {
	skeleton := s
	skeleton.Dictionary = nil
	skeleton.Sources = nil
	skeleton.Segments = nil
	skeleton.Errors = nil
	skeleton.Submissions = nil
	data, err := json.Marshal(document{Version: 2, Summary: summarize(s), Session: skeleton})
	if err != nil {
		return failure("некорректные временные отметки сеанса")
	}
	size := len(data)
	for _, arrayBytes := range []int{arraySize(s.Dictionary), arraySize(s.Sources), arraySize(s.Segments), arraySize(s.Errors), arraySize(s.Submissions)} {
		if arrayBytes < 0 {
			return failure("документ превышает допустимый размер")
		}
		size += arrayBytes - 4
		if size > MaxDocumentBytes {
			return failure("документ превышает допустимый размер")
		}
	}
	return nil
}
func arraySize[T any](items []T) int {
	if items == nil {
		return 4
	}
	size := 2
	for i, item := range items {
		data, err := json.Marshal(item)
		if err != nil {
			return -1
		}
		size += len(data)
		if i > 0 {
			size++
		}
		if size > MaxDocumentBytes {
			return -1
		}
	}
	return size
}

// Association returns the explicitly assigned chat, or an unambiguous legacy
// association inferred from every submission. It never changes the document.
func Association(session Session) string {
	if session.ChatID != "" {
		if idPattern.MatchString(session.ChatID) {
			return session.ChatID
		}
		return ""
	}
	chatID := ""
	for _, submission := range session.Submissions {
		if !idPattern.MatchString(submission.ChatID) {
			return ""
		}
		if chatID != "" && chatID != submission.ChatID {
			return ""
		}
		chatID = submission.ChatID
	}
	return chatID
}

// ForChat reads all matching parts chronologically. Explicit associations need
// only a bounded header; unassigned legacy parts require their body to infer a
// unique association. Unlike List, this query deliberately has no 200-part cap.
func (s *Store) ForChat(chatID string) ([]Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []Summary{}
	if !idPattern.MatchString(chatID) {
		return result, failure("некорректный идентификатор чата")
	}
	root, err := os.OpenRoot(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, failure("не удалось открыть хранилище")
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return result, failure("не удалось прочитать список сеансов")
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return result, failure("не удалось прочитать список сеансов")
	}
	invalid := false
	for _, entry := range entries {
		name := entry.Name()
		id := strings.TrimSuffix(name, ".json")
		if !strings.HasSuffix(name, ".json") || !idPattern.MatchString(id) {
			continue
		}
		info, e := entry.Info()
		if e != nil || !info.Mode().IsRegular() {
			invalid = true
			continue
		}
		sum, e := readSummary(root, name)
		if e != nil {
			invalid = true
			continue
		}
		association := sum.ChatID
		if association == "" {
			session, e := load(root, id)
			if e != nil {
				invalid = true
				continue
			}
			association = Association(session)
		}
		if association == chatID {
			result = append(result, sum)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].StartedAt.Equal(result[j].StartedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].StartedAt.Before(result[j].StartedAt)
	})
	if invalid {
		return result, failure("не удалось прочитать некоторые части истории чата")
	}
	return result, nil
}

// AssignChat explicitly associates an existing unassigned part with a chat.
// Ordinary Save deliberately cannot change this immutable field. Assignment
// preserves all other loaded content and can resolve ambiguous legacy parts.
func (s *Store) AssignChat(id, chatID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !idPattern.MatchString(id) || !idPattern.MatchString(chatID) {
		return failure("некорректный идентификатор сеанса или чата")
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return failure("не удалось открыть хранилище")
	}
	defer root.Close()
	session, err := load(root, id)
	if err != nil {
		return failure("не удалось прочитать сеанс")
	}
	if session.ChatID == chatID {
		return nil
	}
	if session.ChatID != "" {
		return failure("сеанс уже привязан к другому чату")
	}
	session = Clone(session)
	session.ChatID = chatID
	return replaceAssigned(root, session)
}

func encodeDocument(session Session) ([]byte, error) {
	if err := Validate(session); err != nil {
		return nil, err
	}
	data, err := json.Marshal(document{Version: 2, Summary: summarize(session), Session: session})
	if err != nil || len(data) > MaxDocumentBytes {
		return nil, failure("документ превышает допустимый размер")
	}
	return data, nil
}

// The caller holds the directory lock and has loaded the existing identity.
func replaceAssigned(root *os.Root, session Session) error {
	data, err := encodeDocument(session)
	if err != nil {
		return err
	}
	if err = storage.Replace(root, session.ID+".json", data); err != nil {
		return failure("не удалось привязать сеанс к чату")
	}
	return nil
}
