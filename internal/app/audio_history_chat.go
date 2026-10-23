package app

import (
	"encoding/hex"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/97Dmitry/solveo/internal/audiohistory"
)

// Sync presentation/history ownership without holding c.mu and audio.mu together.
func (c *Controller) syncAudioHistoryChat() {
	c.mu.Lock()
	id, revision := c.active.ID, c.revision
	c.mu.Unlock()
	a := &c.audio
	a.mu.Lock()
	defer a.mu.Unlock()
	h := &a.history
	if h.persist == nil || id == "" || revision < h.chatRevision {
		return
	}
	changed := h.chatID != id
	h.chatID, h.chatRevision = id, revision
	if changed {
		a.segments = nil
		a.revisions = map[string]int{}
		h.segments = map[string]string{}
		c.audioChangedLocked()
	}
	old := h.docs[h.current]
	if old == nil || old.session.ChatID == id || (!a.listening && a.busy != audioBusyOpening) {
		return
	}
	provider, label, dictionary, runID := old.session.ProviderID, old.session.ProviderLabel, slices.Clone(old.session.Dictionary), old.runID
	sources := slices.Clone(old.session.Sources)
	c.endAudioHistoryLocked()
	newID := c.beginAudioHistoryLocked(provider, label, dictionary)
	if doc := h.docs[newID]; doc != nil {
		doc.runID = runID
		doc.session.Sources = sources
	}
	for stream, run := range h.streamRuns {
		if run == runID {
			h.streams[stream] = newID
			h.streamChats[stream] = id
		}
	}
}
func (c *Controller) bindAudioHistoryUtterance(stream, id string) {
	c.audio.mu.Lock()
	defer c.audio.mu.Unlock()
	h := &c.audio.history
	if id == "" || h.utterances[id] != "" {
		return
	}
	if session := h.streams[stream]; session != "" {
		h.utterances[id] = session
		h.utteranceChats[id] = h.streamChats[stream]
		h.started[id] = c.deps.Now().UTC().Format("2006-01-02T15:04:05Z07:00")
	}
}
func (c *Controller) pinAudioHistoryUtterance(stream, id string) func() {
	c.audio.mu.Lock()
	session := c.audio.history.utterances[id]
	if session == "" {
		session = c.audio.history.streams[stream]
	}
	c.audio.mu.Unlock()
	return c.pinAudioHistoryDoc(session)
}

// LoadChatAudioHistory merges technical runs; the UI never needs to choose Start/Stop parts.
func (c *Controller) LoadChatAudioHistory(chatID string) (AudioHistoryView, error) {
	out := AudioHistoryView{ID: chatID, ChatID: chatID, Dictionary: []string{}, Sources: []audiohistory.Source{}, Segments: []TranscriptSegment{}, Errors: []AudioHistoryFailure{}, Submissions: []AudioHistorySubmission{}}
	if raw, err := hex.DecodeString(chatID); err != nil || len(raw) != 16 || strings.ToLower(chatID) != chatID {
		return out, errors.New("Некорректный идентификатор чата")
	}
	if c.deps.Audio.History == nil {
		return out, errors.New("История аудио недоступна")
	}
	if err := c.flushAudioHistory(); err != nil {
		return out, err
	}
	parts, err := c.deps.Audio.History.ForChat(chatID)
	if err != nil {
		out.Notice = "Некоторые записи архива не удалось прочитать; история может быть неполной"
	}
	providers := map[string]bool{}
	dictionaries := map[string]bool{}
	sources := map[string]bool{}
	bytes := 0
	unfinished := false
	for _, part := range parts {
		doc, err := c.loadAudioHistory(part.ID)
		if err != nil {
			out.Notice = "Некоторые записи чата не удалось прочитать; история показана не полностью"
			continue
		}
		if out.StartedAt == "" || historyBefore(doc.StartedAt, out.StartedAt) {
			out.StartedAt = doc.StartedAt
		}
		if doc.EndedAt == "" {
			unfinished = true
		} else if historyBefore(out.EndedAt, doc.EndedAt) {
			out.EndedAt = doc.EndedAt
		}
		providers[doc.ProviderLabel] = true
		for _, term := range doc.Dictionary {
			if !dictionaries[term] {
				out.Dictionary = append(out.Dictionary, term)
				dictionaries[term] = true
			}
		}
		for _, source := range doc.Sources {
			key := source.Track + "\x00" + source.Device
			if !sources[key] {
				out.Sources = append(out.Sources, source)
				sources[key] = true
			}
		}
		for _, row := range doc.Segments {
			bytes += len(row.Text) + len(row.ID) + 256
			out.Segments = append(out.Segments, row)
		}
		for _, e := range doc.Errors {
			bytes += len(e.Message) + 128
			out.Errors = append(out.Errors, e)
		}
		for _, event := range doc.Submissions {
			bytes += len(event.SegmentIDs)*256 + 256
			out.Submissions = append(out.Submissions, event)
		}
		if bytes > 32<<20 || len(out.Segments) > 100000 || len(out.Errors) > 10000 || len(out.Submissions) > 10000 || len(out.Dictionary) > 4096 || len(out.Sources) > 128 {
			return AudioHistoryView{}, errors.New("Голосовая история чата слишком велика для загрузки целиком")
		}
	}
	if unfinished {
		out.EndedAt = ""
	}
	var labels []string
	for label := range providers {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	out.ProviderLabel = strings.Join(labels, " / ")
	out.ProviderID = "chat"
	sort.SliceStable(out.Segments, func(i, j int) bool { return historyBefore(out.Segments[i].StartedAt, out.Segments[j].StartedAt) })
	sort.SliceStable(out.Errors, func(i, j int) bool { return historyBefore(out.Errors[i].At, out.Errors[j].At) })
	sort.SliceStable(out.Submissions, func(i, j int) bool { return historyBefore(out.Submissions[i].At, out.Submissions[j].At) })
	return out, nil
}
func (c *Controller) DeleteChatAudioHistory(chatID string) error {
	if c.deps.Audio.History == nil {
		return errors.New("История аудио недоступна")
	}
	c.syncAudioHistoryChat()
	c.audio.mu.Lock()
	active := c.audio.history.docs[c.audio.history.current]
	blocked := active != nil && active.session.ChatID == chatID
	c.audio.mu.Unlock()
	if blocked {
		return errors.New("Сначала остановите запись в этом чате")
	}
	if err := c.flushAudioHistory(); err != nil {
		return err
	}
	parts, err := c.deps.Audio.History.ForChat(chatID)
	if err != nil {
		return err
	}
	for _, part := range parts {
		if err := c.deleteAudioHistory(part.ID); err != nil {
			return err
		}
	}
	return nil
}
func historyBefore(a, b string) bool {
	if b == "" {
		return false
	}
	if a == "" {
		return true
	}
	at, _ := time.Parse(time.RFC3339Nano, a)
	bt, _ := time.Parse(time.RFC3339Nano, b)
	return at.Before(bt)
}
