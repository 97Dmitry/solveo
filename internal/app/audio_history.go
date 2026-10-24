package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/97Dmitry/solveo/internal/audiohistory"
	"github.com/97Dmitry/solveo/internal/chat"
)

type AudioHistoryStore interface {
	Save(audiohistory.Session) error
	Load(string) (audiohistory.Session, error)
	Delete(string) error
	ForChat(string) ([]audiohistory.Summary, error)
}
type AudioHistoryFailure struct {
	At      string `json:"at"`
	Track   string `json:"track"`
	Message string `json:"message"`
}
type AudioHistorySubmission struct {
	At          string   `json:"at"`
	ChatID      string   `json:"chat_id"`
	OperationID string   `json:"operation_id"`
	SegmentIDs  []string `json:"segment_ids"`
}
type AudioHistoryView struct {
	Notice        string                   `json:"notice,omitempty"`
	ChatID        string                   `json:"chat_id"`
	ID            string                   `json:"id"`
	ProviderID    string                   `json:"provider_id"`
	ProviderLabel string                   `json:"provider_label"`
	StartedAt     string                   `json:"started_at"`
	EndedAt       string                   `json:"ended_at"`
	Dictionary    []string                 `json:"dictionary"`
	Sources       []audiohistory.Source    `json:"sources"`
	Segments      []TranscriptSegment      `json:"segments"`
	Errors        []AudioHistoryFailure    `json:"errors"`
	Submissions   []AudioHistorySubmission `json:"submissions"`
}
type audioHistoryDoc struct {
	runID           string
	pins            int
	session         audiohistory.Session
	revision, saved uint64
	seen            map[string]bool
	bytes           int
	limited         bool
}
type audioHistoryState struct {
	utteranceChats map[string]string
	streamChats    map[string]string
	chatID, runID  string
	chatRevision   uint64
	utterances     map[string]string
	started        map[string]string
	streamRuns     map[string]string
	deleted        map[string]bool
	docs           map[string]*audioHistoryDoc
	streams        map[string]string
	segments       map[string]string
	current        string
	err, limit     string
	persist        *persister
}
type audioHistoryWrite struct {
	id       string
	revision uint64
	session  audiohistory.Session
}

func (c *Controller) initAudioHistory() {
	h := &c.audio.history
	h.chatID = c.active.ID
	h.chatRevision = c.revision
	h.utterances = map[string]string{}
	h.utteranceChats = map[string]string{}
	h.streamChats = map[string]string{}
	h.started = map[string]string{}
	h.streamRuns = map[string]string{}
	h.docs = map[string]*audioHistoryDoc{}
	h.deleted = map[string]bool{}
	h.streams = map[string]string{}
	h.segments = map[string]string{}
	h.persist = &persister{delay: time.Second, collect: func() any {
		c.audio.mu.Lock()
		defer c.audio.mu.Unlock()
		var batch []audioHistoryWrite
		for id, doc := range h.docs {
			if doc.revision > doc.saved {
				batch = append(batch, audioHistoryWrite{id: id, revision: doc.revision, session: audiohistory.Clone(doc.session)})
			}
		}
		return batch
	}, save: func(v any) error {
		for _, write := range v.([]audioHistoryWrite) {
			if c.deps.Audio.History == nil {
				continue
			}
			if err := c.deps.Audio.History.Save(write.session); err != nil {
				return err
			}
			c.audio.mu.Lock()
			if doc := h.docs[write.id]; doc != nil && doc.saved < write.revision {
				doc.saved = write.revision
			}
			c.pruneAudioHistoryLocked()
			c.audio.mu.Unlock()
		}
		return nil
	}, report: func(err error) {
		text := ""
		if err != nil {
			text = "История аудио не сохранена: " + err.Error()
		}
		c.audio.mu.Lock()
		if h.err != text {
			h.err = text
			c.audioChangedLocked()
		}
		c.audio.mu.Unlock()
	}}
}
func (c *Controller) beginAudioHistoryLocked(providerID, label string, dictionary []string) string {
	if c.deps.Audio.History == nil {
		return ""
	}
	h := &c.audio.history
	c.pruneAudioHistoryLocked()
	total := 0
	for _, doc := range h.docs {
		total += doc.bytes
	}
	if total+8192 > 64<<20 {
		h.limit = "История новых сеансов приостановлена: восстановите запись на диск"
		return ""
	}
	if h.current != "" {
		c.endAudioHistoryLocked()
	}
	id := chat.ID()
	h.docs[id] = &audioHistoryDoc{runID: h.runID, session: audiohistory.Session{ChatID: h.chatID, ID: id, ProviderID: providerID, ProviderLabel: label, StartedAt: c.deps.Now().UTC(), Dictionary: slices.Clone(dictionary), Sources: []audiohistory.Source{}, Segments: []audiohistory.Segment{}, Errors: []audiohistory.Failure{}, Submissions: []audiohistory.Submission{}}, revision: 1, seen: map[string]bool{}, bytes: 8192}
	h.current = id
	h.limit = ""
	h.persist.mark()
	return id
}
func (c *Controller) endAudioHistoryLocked() {
	h := &c.audio.history
	if doc := h.docs[h.current]; doc != nil {
		doc.session.EndedAt = c.deps.Now().UTC()
		doc.revision++
		h.persist.mark()
	}
	h.current = ""
}
func (c *Controller) historyBudgetLocked(doc *audioHistoryDoc, value any) bool {
	if doc.limited {
		return false
	}
	data, err := json.Marshal(value)
	if err != nil || doc.bytes+len(data) > 16<<20 || c.audioHistoryBytesLocked()+len(data) > 64<<20 {
		doc.limited = true
		c.audio.history.limit = "Достигнут лимит истории сеанса; новые реплики остаются только на экране"
		if len(doc.session.Errors) < 500 {
			doc.session.Errors = append(doc.session.Errors, audiohistory.Failure{At: c.deps.Now().UTC(), Message: c.audio.history.limit})
		}
		doc.revision++
		c.audio.history.persist.mark()
		return false
	}
	doc.bytes += len(data) + 128
	return true
}
func (c *Controller) recordAudioFinalLocked(stream string, segment TranscriptSegment) {
	if c.logContent.Load() {
		c.logger.Info("transcript final", "track", segment.Track, "utterance", segment.ID, c.content("text", segment.Text))
	}
	h := &c.audio.history
	id := h.utterances[segment.ID]
	if id == "" {
		id = h.streams[stream]
	}
	doc := h.docs[id]
	if doc == nil || doc.seen[segment.ID] || segment.Text == "" {
		return
	}
	started, err := time.Parse(time.RFC3339, segment.StartedAt)
	if err != nil {
		started = c.deps.Now().UTC()
	}
	row := audiohistory.Segment{ID: segment.ID, StreamID: stream, Track: segment.Track, Text: segment.Text, StartedAt: started, FinalAt: c.deps.Now().UTC()}
	if len(doc.session.Segments) >= audiohistory.MaxSegments {
		message := "Достигнут лимит 10000 реплик в сеансе; новые реплики остаются только на экране"
		h.limit = message
		if len(doc.session.Errors) < 500 && (len(doc.session.Errors) == 0 || doc.session.Errors[len(doc.session.Errors)-1].Message != message) {
			doc.session.Errors = append(doc.session.Errors, audiohistory.Failure{At: c.deps.Now().UTC(), Message: message})
			doc.revision++
			h.persist.mark()
		}
		return
	}
	if err := audiohistory.ValidateSegment(row); err != nil {
		h.limit = "Не удалось сохранить реплику: " + err.Error()
		return
	}
	if !c.historyBudgetLocked(doc, row) {
		return
	}
	doc.session.Segments = append(doc.session.Segments, row)
	doc.seen[row.ID] = true
	h.segments[row.ID] = id
	doc.revision++
	h.persist.mark()
}
func (c *Controller) recordAudioErrorLocked(stream, message string) {
	c.logger.Warn("speech error", "track", c.audio.tracks[stream], "error", message)
	h := &c.audio.history
	id := h.streams[stream]
	if id == "" {
		id = h.current
		if id == "" && len(c.audio.sources) > 0 {
			id = h.streams[c.audio.sources[0].stream]
		}
	}
	doc := h.docs[id]
	if doc == nil || len(doc.session.Errors) >= 500 {
		return
	}
	event := audiohistory.Failure{At: c.deps.Now().UTC(), Track: c.audio.tracks[stream], Message: uiText(message)}
	if !c.historyBudgetLocked(doc, event) {
		return
	}
	doc.session.Errors = append(doc.session.Errors, event)
	doc.revision++
	h.persist.mark()
}
func historyTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
func (c *Controller) loadAudioHistory(id string) (AudioHistoryView, error) {
	out := AudioHistoryView{}
	if c.deps.Audio.History == nil {
		return out, errors.New("История аудио недоступна")
	}
	s, err := c.deps.Audio.History.Load(id)
	if err != nil {
		return out, err
	}
	out = AudioHistoryView{ChatID: s.ChatID, ID: s.ID, ProviderID: s.ProviderID, ProviderLabel: s.ProviderLabel, StartedAt: historyTime(s.StartedAt), EndedAt: historyTime(s.EndedAt), Dictionary: append([]string{}, s.Dictionary...), Sources: append([]audiohistory.Source{}, s.Sources...), Segments: []TranscriptSegment{}, Errors: []AudioHistoryFailure{}, Submissions: []AudioHistorySubmission{}}
	for _, r := range s.Segments {
		out.Segments = append(out.Segments, TranscriptSegment{ID: r.ID, Track: r.Track, StartedAt: historyTime(r.StartedAt), Text: r.Text, Final: true, ProviderLabel: s.ProviderLabel})
	}
	for _, e := range s.Errors {
		out.Errors = append(out.Errors, AudioHistoryFailure{At: historyTime(e.At), Track: e.Track, Message: e.Message})
	}
	for _, e := range s.Submissions {
		out.Submissions = append(out.Submissions, AudioHistorySubmission{At: historyTime(e.At), ChatID: e.ChatID, OperationID: e.OperationID, SegmentIDs: append([]string{}, e.SegmentIDs...)})
	}
	return out, nil
}
func (c *Controller) deleteAudioHistory(id string) error {
	if c.deps.Audio.History == nil {
		return errors.New("История аудио недоступна")
	}
	h := &c.audio.history
	h.persist.writeMu.Lock()
	defer h.persist.writeMu.Unlock()
	c.audio.mu.Lock()
	active := h.current == id && id != ""
	c.audio.mu.Unlock()
	if active {
		return errors.New("Сначала остановите текущий аудиосеанс")
	}
	if err := c.deps.Audio.History.Delete(id); err != nil {
		return err
	}
	c.audio.mu.Lock()
	delete(h.docs, id)
	h.deleted[id] = true
	for segment, session := range h.segments {
		if session == id {
			delete(h.segments, segment)
		}
	}
	c.audio.mu.Unlock()
	return nil
}
func (c *Controller) recordAudioSubmission(groups map[string][]string, chatID, operationID string) {
	var releases []func()
	for id := range groups {
		if release := c.pinAudioHistoryDoc(id); release != nil {
			releases = append(releases, release)
		}
	}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	c.audio.mu.Lock()
	defer c.audio.mu.Unlock()
	for id, ids := range groups {
		doc := c.audio.history.docs[id]
		if doc == nil || len(doc.session.Submissions) >= 1000 {
			continue
		}
		event := audiohistory.Submission{At: c.deps.Now().UTC(), ChatID: chatID, OperationID: operationID, SegmentIDs: slices.Clone(ids)}
		if !c.historyBudgetLocked(doc, event) {
			continue
		}
		doc.session.Submissions = append(doc.session.Submissions, event)
		doc.revision++
		c.audio.history.persist.mark()
	}
}
func (c *Controller) flushAudioHistory() error {
	if c.audio.history.persist == nil {
		return nil
	}
	if err := c.audio.history.persist.flush(); err != nil {
		return fmt.Errorf("история аудио не сохранена: %w", err)
	}
	return nil
}

func (c *Controller) audioHistoryBytesLocked() int {
	total := 0
	for _, doc := range c.audio.history.docs {
		total += doc.bytes
	}
	return total
}

// Keep a small saved-session cache; dirty/current documents stay resident until safely written.
func (c *Controller) pruneAudioHistoryLocked() {
	h := &c.audio.history
	var candidates []string
	for id, doc := range h.docs {
		if id != h.current && !doc.session.EndedAt.IsZero() && doc.saved == doc.revision && doc.pins == 0 {
			candidates = append(candidates, id)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return h.docs[candidates[i]].session.StartedAt.Before(h.docs[candidates[j]].session.StartedAt)
	})
	total := c.audioHistoryBytesLocked()
	for _, id := range candidates {
		if len(h.docs) <= 4 && total <= 32<<20 {
			break
		}
		total -= h.docs[id].bytes
		delete(h.docs, id)
	}
}

func (c *Controller) pinAudioHistoryStream(stream string) func() {
	c.audio.mu.Lock()
	id := c.audio.history.streams[stream]
	c.audio.mu.Unlock()
	return c.pinAudioHistoryDoc(id)
}
func (c *Controller) historyRelease(id string, doc *audioHistoryDoc) func() {
	return func() {
		c.audio.mu.Lock()
		if c.audio.history.docs[id] == doc {
			doc.pins--
			c.pruneAudioHistoryLocked()
		}
		c.audio.mu.Unlock()
	}
}

// Pins cover reload through mutation. writeMu serializes reload/install against save/eviction/delete.
func (c *Controller) pinAudioHistoryDoc(id string) func() {
	if id == "" || c.deps.Audio.History == nil {
		return nil
	}
	h := &c.audio.history
	c.audio.mu.Lock()
	if doc := h.docs[id]; doc != nil && !h.deleted[id] {
		doc.pins++
		c.audio.mu.Unlock()
		return c.historyRelease(id, doc)
	}
	deleted := h.deleted[id]
	c.audio.mu.Unlock()
	if deleted {
		return nil
	}
	h.persist.writeMu.Lock()
	defer h.persist.writeMu.Unlock()
	c.audio.mu.Lock()
	if h.deleted[id] {
		c.audio.mu.Unlock()
		return nil
	}
	if doc := h.docs[id]; doc != nil {
		doc.pins++
		c.audio.mu.Unlock()
		return c.historyRelease(id, doc)
	}
	c.audio.mu.Unlock()
	session, err := c.deps.Audio.History.Load(id)
	if err != nil {
		c.audio.mu.Lock()
		if !h.deleted[id] {
			h.limit = "Не удалось дополнить историю сеанса: " + err.Error()
			c.audioChangedLocked()
		}
		c.audio.mu.Unlock()
		return nil
	}
	seen := map[string]bool{}
	for _, row := range session.Segments {
		seen[row.ID] = true
	}
	data, _ := json.Marshal(session)
	c.audio.mu.Lock()
	defer c.audio.mu.Unlock()
	if h.deleted[id] {
		return nil
	}
	c.pruneAudioHistoryLocked()
	if c.audioHistoryBytesLocked()+len(data)+8192 > 64<<20 {
		h.limit = "Недостаточно памяти для дополнения истории; восстановите запись на диск"
		c.audioChangedLocked()
		return nil
	}
	doc := &audioHistoryDoc{session: session, revision: 1, saved: 1, seen: seen, bytes: len(data) + 8192, pins: 1}
	h.docs[id] = doc
	return c.historyRelease(id, doc)
}
