// Package questionbuilder owns a bounded, deterministic question-card state machine.
// Callers serialize access; timers, provider calls and persistence belong to the application.
package questionbuilder

import (
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxSegments = 80
	MaxText     = 16000
	MaxCard     = 8000
	Window      = 120 * time.Second
)

type Segment struct {
	ID    string `json:"id"`
	Track string `json:"track"`
	Text  string `json:"text"`
	At    string `json:"at"`
	Final bool   `json:"final"`
	Seq   uint64 `json:"seq"`
}

const (
	OriginAutomatic = "automatic"
	OriginManual    = "manual"
)

type Card struct {
	Origin             string    `json:"origin,omitempty"`
	ID                 string    `json:"id"`
	Version            uint64    `json:"version"`
	Text               string    `json:"text"`
	Sources            []Segment `json:"sources"`
	PreviousQuestionID string    `json:"previous_question_id"`
}
type Input struct {
	ProcessedThrough uint64    `json:"processed_through"`
	Token            uint64    `json:"-"`
	End              uint64    `json:"-"`
	ChatID           string    `json:"chat_id"`
	Segments         []Segment `json:"segments"`
	Current          Card      `json:"current"`
	Recent           []Card    `json:"recent"`
	Context          string    `json:"context"`
}
type Result struct {
	HasQuestion bool     `json:"has_question"`
	Ready       bool     `json:"ready"`
	Text        string   `json:"text"`
	SourceIDs   []string `json:"source_ids"`
	FollowUp    bool     `json:"follow_up"`
}
type View struct {
	Card
	ChatID     string `json:"chat_id"`
	Enabled    bool   `json:"enabled"`
	Status     string `json:"status"`
	Error      string `json:"error"`
	CanSend    bool   `json:"can_send"`
	CanConfirm bool   `json:"can_confirm"`
	Manual     bool   `json:"manual"`
	Editing    bool   `json:"editing"`
	Unincluded bool   `json:"unincluded"`
	Revision   uint64 `json:"revision"`
}
type Builder struct {
	covered                                      uint64
	overflow                                     bool
	chatID                                       string
	current                                      Card
	recent                                       []Card
	window                                       []Segment
	partial                                      map[string]bool
	ignored                                      map[string]bool
	enabled, manual, editing, ready, pausedStale bool
	err                                          string
	seq, processed, token, revision              uint64
	running                                      *Input
	force                                        bool
}

func New(chatID string) *Builder {
	return &Builder{chatID: chatID, partial: map[string]bool{}, ignored: map[string]bool{}, current: Card{Sources: []Segment{}}, revision: 1}
}
func cloneCard(c Card) Card {
	c.Sources = slices.Clone(c.Sources)
	if c.Sources == nil {
		c.Sources = []Segment{}
	}
	return c
}
func (b *Builder) invalidate() { b.token++; b.running = nil; b.revision++ }
func (b *Builder) Enable(on bool) {
	if b.enabled == on {
		return
	}
	if !on && len(b.partial) > 0 {
		b.pausedStale = true
	}
	if on && b.current.ID == "" && b.covered == 0 {
		b.pausedStale = false
	}
	b.enabled = on
	b.invalidate()
	// A partly spoken phrase belongs to its original listening interval.
	for id := range b.partial {
		b.ignored[id] = true
	}
	clear(b.partial)
}
func (b *Builder) Exclude(ids []string) {
	for _, id := range ids {
		b.ignored[id] = true
	}
}
func (b *Builder) Observe(s Segment) {
	if b.ignored[s.ID] {
		if s.Track == "system" && b.current.ID != "" {
			b.pausedStale = true
			b.revision++
		}
		if s.Final {
			delete(b.ignored, s.ID)
		}
		return
	}
	if !b.enabled {
		if s.Track == "system" && (b.current.ID != "" || b.covered > 0) {
			b.pausedStale = true
			b.revision++
		}
		if !s.Final {
			b.ignored[s.ID] = true
		}
		return
	}
	if !s.Final {
		if s.Track == "system" {
			b.partial[s.ID] = true
			b.revision++
		}
		return
	}
	delete(b.partial, s.ID)
	if strings.TrimSpace(s.Text) == "" {
		b.revision++
		return
	}
	for _, old := range b.window {
		if old.ID == s.ID {
			return
		}
	}
	b.seq++
	s.Seq = b.seq
	if utf8.RuneCountInString(s.Text) > MaxText {
		b.stopOverflow()
		return
	}
	b.window = append(b.window, s)
	b.revision++
	at, _ := time.Parse(time.RFC3339Nano, s.At)
	b.trim(at)
}
func (b *Builder) trim(now time.Time) {
	size := func() int {
		n := 0
		for _, s := range b.window {
			n += utf8.RuneCountInString(s.Text)
		}
		return n
	}
	for len(b.window) > 0 && (len(b.window) > MaxSegments || size() > MaxText || now.Sub(segmentTime(b.window[0])) > Window) {
		if b.window[0].Seq > b.processed {
			b.stopOverflow()
			return
		}
		b.window = b.window[1:]
	}
}
func (b *Builder) pendingSystem() bool {
	for _, s := range b.window {
		if s.Seq > b.processed && s.Track == "system" {
			return true
		}
	}
	return false
}
func (b *Builder) Begin(now time.Time) (Input, bool) {
	if !b.enabled || b.running != nil || b.manual || b.editing || b.err != "" {
		return Input{}, false
	}
	b.trim(now)
	if b.err != "" || (!b.force && !b.pendingSystem()) {
		return Input{}, false
	}
	b.force = false
	b.token++
	b.revision++
	in := Input{ProcessedThrough: b.processed, Token: b.token, End: b.seq, ChatID: b.chatID, Segments: slices.Clone(b.window), Current: cloneCard(b.current), Recent: []Card{}}
	for _, c := range b.recent {
		in.Recent = append(in.Recent, cloneCard(c))
	}
	b.covered = in.End
	b.running = &in
	return in, true
}
func Validate(in Input, r Result) error {
	if !utf8.ValidString(r.Text) || utf8.RuneCountInString(r.Text) > MaxCard || len(r.SourceIDs) > MaxSegments {
		return errors.New("некорректный размер результата сборщика")
	}
	if !r.HasQuestion {
		if r.Ready || strings.TrimSpace(r.Text) != "" || len(r.SourceIDs) > 0 || r.FollowUp {
			return errors.New("противоречивый результат сборщика")
		}
		return nil
	}
	if strings.TrimSpace(r.Text) == "" || len(r.SourceIDs) == 0 {
		return errors.New("сборщик вернул вопрос без текста или оснований")
	}
	allowed := map[string]Segment{}
	for _, s := range in.Segments {
		allowed[s.ID] = s
	}
	for _, s := range in.Current.Sources {
		allowed[s.ID] = s
	}
	system := false
	seen := map[string]bool{}
	for _, id := range r.SourceIDs {
		s, ok := allowed[id]
		if !ok || seen[id] {
			return fmt.Errorf("некорректное основание вопроса: %s", id)
		}
		seen[id] = true
		system = system || s.Track == "system"
	}
	if !system {
		return errors.New("вопрос не опирается на системную речь")
	}
	if r.FollowUp && len(in.Recent) == 0 {
		return errors.New("у уточнения нет предыдущего вопроса")
	}
	return nil
}
func (b *Builder) Apply(in Input, r Result, err error) error {
	if b.running == nil || in.Token != b.token {
		return nil
	}
	b.running = nil
	b.revision++
	if err == nil {
		err = Validate(in, r)
	}
	if err != nil {
		b.err = err.Error()
		return err
	}
	if r.HasQuestion {
		sources := slices.Clone(in.Current.Sources)
		seen := map[string]bool{}
		for _, s := range sources {
			seen[s.ID] = true
		}
		all := append(slices.Clone(in.Segments), in.Current.Sources...)
		for _, id := range r.SourceIDs {
			if seen[id] {
				continue
			}
			for _, s := range all {
				if s.ID == id {
					sources = append(sources, s)
					seen[id] = true
					break
				}
			}
		}
		if err := ValidateSources(sources); err != nil {
			b.err = err.Error()
			return err
		}
		if b.current.ID == "" {
			b.current.ID = rand.Text()
		}
		b.current.Version++
		b.current.Origin = OriginAutomatic
		b.current.Text = r.Text
		b.current.Sources = sources
		b.ready = r.Ready
		if r.FollowUp {
			b.current.PreviousQuestionID = b.recent[len(b.recent)-1].ID
		}
	}
	b.processed = in.End
	b.trim(time.Now())
	return nil
}
func (b *Builder) Edit(text string) error {
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > MaxCard {
		return errors.New("вопрос длиннее 8000 символов")
	}
	if !b.editing && !b.manual && b.current.ID != "" {
		b.covered = b.processed
	}
	if !b.editing && !b.manual && b.covered == 0 {
		b.covered = b.seq
	}
	b.invalidate()
	if b.current.ID == "" {
		for _, s := range b.window {
			if s.Seq <= b.covered && len(b.current.Sources) < MaxSegments {
				b.current.Sources = append(b.current.Sources, s)
			}
		}
	}
	if b.current.ID == "" {
		b.current.ID = rand.Text()
	}
	b.current.Version++
	b.current.Text = text
	b.current.Origin = OriginManual
	b.editing = true
	b.manual = false
	return nil
}
func (b *Builder) SaveEdit() error {
	if !b.editing || strings.TrimSpace(b.current.Text) == "" {
		return errors.New("нет непустой ручной правки")
	}
	b.editing = false
	b.manual = true
	b.ready = true
	b.err = ""
	b.revision++
	return nil
}

// Confirm accepts a completed model draft only after a fresh explicit user review.
// It freezes the displayed version without changing text provenance or sending a turn.
func (b *Builder) Confirm() error {
	if !b.View().CanConfirm {
		return errors.New("Черновик ещё анализируется, имеет новые системные реплики или требует ручной правки")
	}
	b.invalidate()
	b.current.Version++
	b.manual = true
	b.ready = true
	return nil
}

func (b *Builder) Rebuild() {
	if b.overflow {
		return
	}
	b.invalidate()
	b.manual = false
	b.editing = false
	b.err = ""
	b.force = true
}
func (b *Builder) Retry() {
	if b.overflow {
		return
	}
	b.err = ""
	b.force = true
	b.revision++
}
func (b *Builder) Skip() {
	end := b.covered
	if b.running != nil {
		end = b.running.End
	}
	if end == 0 {
		end = b.seq
	}
	b.invalidate()
	b.processed = end
	b.covered = 0
	b.overflow = false
	b.current = Card{Sources: []Segment{}}
	b.manual = false
	b.editing = false
	b.ready = false
	b.err = ""
	b.pausedStale = false
	b.window = slices.DeleteFunc(b.window, func(s Segment) bool { return s.Seq <= end })
}
func (b *Builder) Sent(messageID string) {
	c := cloneCard(b.current)
	c.ID = messageID
	c.Sources = []Segment{}
	b.recent = append(b.recent, c)
	if len(b.recent) > 3 {
		b.recent = b.recent[len(b.recent)-3:]
	}
	// A saved manual version consumes only analyzed input; later speech survives.
	b.Skip()
}
func (b *Builder) RestoreRecent(cards []Card) {
	b.recent = nil
	for _, c := range cards {
		c.Sources = []Segment{}
		b.recent = append(b.recent, cloneCard(c))
	}
	if len(b.recent) > 3 {
		b.recent = b.recent[len(b.recent)-3:]
	}
}
func (b *Builder) View() View {
	v := View{Card: cloneCard(b.current), ChatID: b.chatID, Enabled: b.enabled, Status: "waiting", Error: b.err, Manual: b.manual, Editing: b.editing, Revision: b.revision}
	if b.pausedStale && b.current.ID != "" && !b.manual && v.Error == "" {
		v.Error = "Во время паузы появилась системная речь. Сохраните вопрос вручную или пропустите"
	}
	v.Unincluded = b.seq > b.processed || len(b.partial) > 0 || b.pausedStale
	hasText := b.current.ID != "" && strings.TrimSpace(b.current.Text) != ""
	fresh := b.err == "" && b.running == nil && !b.pendingSystem() && len(b.partial) == 0 && !b.pausedStale
	v.CanSend = b.ready && hasText && !b.editing && (b.manual || fresh)
	v.CanConfirm = hasText && !b.ready && !b.manual && !b.editing && fresh
	switch {
	case b.editing:
		v.Status = "editing"
	case v.Error != "":
		v.Status = "error"
	case b.running != nil:
		v.Status = "analyzing"
	case v.CanSend:
		v.Status = "ready"
	case b.current.Text != "":
		v.Status = "draft"
	}
	return v
}

func segmentTime(s Segment) time.Time { t, _ := time.Parse(time.RFC3339Nano, s.At); return t }

func (b *Builder) stopOverflow() {
	b.err = "Сборщик остановлен: переполнено окно необработанной речи. Продолжение доступно в транскрипции; сохраните вопрос вручную или пропустите и включите сборщик заново"
	b.enabled = false
	b.overflow = true
	if !b.manual && !b.editing {
		b.covered = b.seq
	}
	b.invalidate()
}

// ValidateSources bounds the original conditions sent alongside an automatic card.
// Reject the update atomically instead of silently shortening its evidence.
func ValidateSources(sources []Segment) error {
	if len(sources) > MaxSegments {
		return errors.New("Слишком много оснований вопроса; сохраните вручную или пропустите")
	}
	size := 0
	for _, s := range sources {
		if !utf8.ValidString(s.Text) {
			return errors.New("Некорректный UTF-8 в исходных репликах вопроса")
		}
		size += utf8.RuneCountInString(s.Text)
		if size > MaxText {
			return errors.New("Исходные условия вопроса длиннее 16000 символов; сохраните вручную или пропустите")
		}
	}
	return nil
}
