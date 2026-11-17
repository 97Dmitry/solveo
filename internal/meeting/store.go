package meeting

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/97Dmitry/solveo/internal/chat"
)

var filePattern = regexp.MustCompile(`^(system|mic)-[0-9]{4}\.wav$`)
var errArchive = errors.New("Не удалось прочитать архив встречи")

type Store struct {
	dir string
	mu  sync.Mutex
}

func NewStore(dir string) *Store { return &Store{dir: dir} }
func validID(id string) bool {
	b, e := hex.DecodeString(id)
	return e == nil && len(b) == 16 && id == strings.ToLower(id)
}
func validTitle(title string) bool {
	return strings.TrimSpace(title) != "" && utf8.RuneCountInString(title) <= 200
}
func (s *Store) New(title, chatID string, tracks []string) (Document, error) {
	if !validTitle(title) || (chatID != "" && !validID(chatID)) || len(tracks) == 0 || len(tracks) > 2 {
		return Document{}, errors.New("Некорректные параметры встречи")
	}
	for i, t := range tracks {
		if t != "system" && t != "mic" || slices.Contains(tracks[:i], t) {
			return Document{}, errors.New("Некорректный источник")
		}
	}
	d := Document{Info: Info{ID: chat.ID(), ChatID: chatID, Title: strings.TrimSpace(title), StartedAt: chat.Now(), Status: "recording", Revision: 1}, Version: Version, Tracks: slices.Clone(tracks), Parts: []Part{}, Segments: []Segment{}, Leaves: map[string]Summary{}}
	if e := s.Save(d); e != nil {
		return Document{}, e
	}
	return d, nil
}
func (s *Store) root(create bool) (*os.Root, error) {
	if create {
		if e := os.MkdirAll(s.dir, 0700); e != nil {
			return nil, e
		}
	}
	return os.OpenRoot(s.dir)
}
func (s *Store) load(root *os.Root, id string) (Document, error) {
	var d Document
	if !validID(id) {
		return d, errArchive
	}
	f, e := root.Open(id + "/meeting.json")
	if e != nil {
		return d, errArchive
	}
	defer func() { _ = f.Close() }()
	b, e := io.ReadAll(io.LimitReader(f, MaxDocument+1))
	if e != nil || len(b) > MaxDocument {
		return d, errArchive
	}
	if json.Unmarshal(b, &d) != nil || d.ID != id || validate(d) != nil {
		return Document{}, errArchive
	}
	return d, nil
}
func validate(d Document) error {
	if d.Version != Version || !validID(d.ID) || !validTitle(d.Title) || d.Revision == 0 || d.StartedAt.IsZero() || len(d.Parts) > 400 || len(d.Segments) > 300000 {
		return errArchive
	}
	if d.ChatID != "" && !validID(d.ChatID) {
		return errArchive
	}
	if d.Status != "recording" && d.Status != "recorded" && d.Status != "interrupted" {
		return errArchive
	}
	files := map[string]Part{}
	for _, p := range d.Parts {
		if !filePattern.MatchString(p.File) || !strings.HasPrefix(p.File, p.Track+"-") || p.Rate < 8000 || p.Rate > 192000 || p.Samples < 0 || p.Samples > int64(p.Rate)*PartSeconds || p.Offset < 0 || math.IsNaN(p.Offset) || math.IsInf(p.Offset, 0) {
			return errArchive
		}
		if _, ok := files[p.File]; ok {
			return errArchive
		}
		files[p.File] = p
	}
	ids := map[string]bool{}
	for _, seg := range d.Segments {
		p, ok := files[seg.Part]
		if !ok || seg.Track != p.Track || seg.ID == "" || ids[seg.ID] || strings.TrimSpace(seg.Text) == "" || !finite(seg.Start) || !finite(seg.End) || seg.Start < p.Offset || seg.End < seg.Start || seg.End > p.Offset+float64(p.Samples)/float64(p.Rate)+.01 {
			return errArchive
		}
		ids[seg.ID] = true
	}
	return nil
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func (s *Store) Load(id string) (Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, e := s.root(false)
	if e != nil {
		return Document{}, errArchive
	}
	defer func() { _ = r.Close() }()
	return s.load(r, id)
}
func (s *Store) Save(d Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if validate(d) != nil {
		return errArchive
	}
	r, e := s.root(true)
	if e != nil {
		return errors.New("Не удалось открыть архив встреч")
	}
	defer func() { _ = r.Close() }()
	if _, e = r.Stat(d.ID + "/meeting.json"); e == nil {
		old, err := s.load(r, d.ID)
		if err != nil {
			return err
		}
		if old.ChatID != d.ChatID || !old.StartedAt.Equal(d.StartedAt) {
			return errArchive
		}
	} else if !os.IsNotExist(e) {
		return errArchive
	}
	if e = r.MkdirAll(d.ID, 0700); e != nil {
		return errArchive
	}
	data, e := json.Marshal(d)
	if e != nil || len(data) > MaxDocument {
		return errors.New("Архив встречи превышает ограничение")
	}
	f, e := r.OpenFile(d.ID+"/meeting.tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return errArchive
	}
	_, e = f.Write(data)
	e = errors.Join(e, f.Sync(), f.Close())
	if e != nil {
		return errors.New("Не удалось сохранить встречу")
	}
	if e = r.Rename(d.ID+"/meeting.tmp", d.ID+"/meeting.json"); e != nil {
		return errors.New("Не удалось сохранить встречу")
	}
	return nil
}
func (s *Store) List() ([]Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Info{}
	r, e := s.root(false)
	if os.IsNotExist(e) {
		return out, nil
	}
	if e != nil {
		return out, errArchive
	}
	defer func() { _ = r.Close() }()
	f, e := r.Open(".")
	if e != nil {
		return out, errArchive
	}
	entries, e := f.ReadDir(-1)
	_ = f.Close()
	if e != nil {
		return out, errArchive
	}
	var notice error
	for _, entry := range entries {
		if !validID(entry.Name()) {
			continue
		}
		v, e := readInfo(r, entry.Name())
		if e != nil {
			notice = errArchive
			continue
		}
		if v.Status == "recording" {
			v.Status = "interrupted"
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, notice
}
func (s *Store) OpenAudio(id, file string) (*os.File, error) {
	if !validID(id) || !filePattern.MatchString(file) {
		return nil, errArchive
	}
	d, e := s.Load(id)
	if e != nil {
		return nil, e
	}
	if !slices.ContainsFunc(d.Parts, func(p Part) bool { return p.File == file }) {
		return nil, errArchive
	}
	r, e := s.root(false)
	if e != nil {
		return nil, errArchive
	}
	defer func() { _ = r.Close() }()
	f, e := r.Open(id + "/" + file)
	if e != nil {
		return nil, errArchive
	}
	return f, nil
}
func (s *Store) Recover(id string) (Document, error) {
	d, e := s.Load(id)
	if e != nil {
		return d, e
	}
	r, e := s.root(false)
	if e != nil {
		return d, errArchive
	}
	defer func() { _ = r.Close() }()

	recovered := make([]Part, 0, len(d.Parts))
	for _, p := range d.Parts {
		if p.Complete {
			recovered = append(recovered, p)
			continue
		}
		f, err := r.OpenFile(id+"/"+p.File, os.O_RDWR, 0)
		if os.IsNotExist(err) && p.Samples == 0 {
			continue
		}
		if err != nil {
			return d, errArchive
		}
		stat, err := f.Stat()
		if err == nil && stat.Size() < 44 && p.Samples == 0 {
			_ = f.Close()
			continue
		}
		n, err := repairFile(f)
		_ = f.Close()
		if err != nil {
			return d, errArchive
		}
		p.Samples = n
		p.Complete = true
		recovered = append(recovered, p)
	}
	d.Parts = recovered

	d.Status = "interrupted"
	d.Error = "Запись прервана; восстановлены доступные данные"
	d.EndedAt = chat.Now()
	d.Revision++
	measure(&d)
	return d, s.Save(d)
}
func measure(d *Document) {
	d.Bytes = 0
	d.Duration = 0
	for _, p := range d.Parts {
		d.Bytes += 44 + p.Samples*2
		d.Duration = max(d.Duration, p.Offset+float64(p.Samples)/float64(p.Rate))
	}
}
func (s *Store) Folder(id string) (string, error) {
	if _, e := s.Load(id); e != nil {
		return "", e
	}
	return filepath.Join(s.dir, id), nil
}
func partName(track string, index int) string { return fmt.Sprintf("%s-%04d.wav", track, index) }

// Info precedes version in our manifest. Read only this bounded prefix for list polling.
func readInfo(root *os.Root, id string) (Info, error) {
	var info Info
	f, e := root.Open(id + "/meeting.json")
	if e != nil {
		return info, errArchive
	}
	defer func() { _ = f.Close() }()
	decoder := json.NewDecoder(io.LimitReader(f, 16384))
	token, e := decoder.Token()
	if e != nil || token != json.Delim('{') {
		return info, errArchive
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, e = decoder.Token()
		if e != nil {
			return info, errArchive
		}
		key, ok := token.(string)
		if !ok {
			return info, errArchive
		}
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return info, errArchive
		}
		fields[key] = raw
		if key == "version" {
			var version int
			if json.Unmarshal(raw, &version) != nil || version != Version {
				return info, errArchive
			}
			b, _ := json.Marshal(fields)
			if json.Unmarshal(b, &info) != nil || info.ID != id || !validTitle(info.Title) || info.StartedAt.IsZero() || info.Revision == 0 {
				return Info{}, errArchive
			}
			return info, nil
		}
	}
	return info, errArchive
}
