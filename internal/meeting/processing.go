package meeting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

func (m *Manager) Process(parent context.Context, id, kind string, partial bool) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if kind != "transcribe" && kind != "summary" {
		return errors.New("Неизвестная операция встречи")
	}
	if m.processor == nil {
		return errors.New("Обработка Gemini недоступна")
	}
	if e := m.mutable(id); e != nil {
		return e
	}
	state := m.State()
	if state.Paused != nil && state.Paused.ID == id && state.Paused.Kind == kind {
		if at, e := time.Parse(time.RFC3339Nano, state.Paused.RetryAt); e == nil && time.Now().Before(at) {
			return errors.New("Gemini просит подождать до указанного времени перед продолжением")
		}
	}
	d, e := m.store.Load(id)
	if e != nil {
		return e
	}
	if d.Status == "recording" {
		return errors.New("Сначала восстановите прерванную запись")
	}
	if len(d.Parts) == 0 {
		return errors.New("В записи нет аудиоданных")
	}
	for _, p := range d.Parts {
		if !p.Complete {
			return errors.New("Сначала восстановите аудиозапись")
		}
	}
	if kind == "summary" {
		incomplete := d.Status != "recorded"
		for _, p := range d.Parts {
			incomplete = incomplete || !p.Transcribed
		}
		if incomplete && !partial {
			return errors.New("Транскрипт неполный: явно выберите summary доступной части")
		}
		if len(d.Segments) == 0 {
			return errors.New("Нет текста для summary")
		}
	}
	m.mu.Lock()
	if m.jobDone != nil || m.closed {
		m.mu.Unlock()
		return errors.New("Обработка уже выполняется")
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	m.jobCancel = cancel
	m.jobDone = done
	m.mu.Unlock()
	m.changed(func(s *State) {
		s.ProcessingID = id
		s.Stage = kind
		s.Error = ""
		s.Notice = ""
		s.Paused = nil
		s.Completed = 0
		s.Total = len(d.Parts)
	})
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		defer cancel()
		var err error
		if kind == "transcribe" {
			err = m.transcribe(ctx, &d)
		} else {
			err = m.summarize(ctx, &d, partial)
		}
		m.changed(func(s *State) { s.Stage = "cleanup" })
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		cleanupErr := m.processor.Cleanup(cleanupCtx)
		cleanupCancel()

		m.mu.Lock()
		if m.jobDone == done {
			m.jobCancel = nil
			m.jobDone = nil
			m.state.ProcessingID = ""
			m.state.Stage = ""
			if cleanupErr != nil {
				m.state.Notice = "Очистка временных файлов Google отложена. Сохранённые результаты доступны; удаление повторится при следующей обработке."
			}
			m.state.Revision++

			if err != nil {
				var limit *RetryableError
				switch {
				case errors.As(err, &limit):
					paused := &PausedOperation{ID: id, Kind: kind, Partial: partial, Reason: limit.Error()}
					if limit.RetryAfter > 0 {
						paused.RetryAt = time.Now().UTC().Add(limit.RetryAfter).Format(time.RFC3339Nano)
					}
					m.state.Paused = paused
					m.state.Stage = "paused"
				case errors.Is(err, context.Canceled):
					m.state.Error = "Обработка отменена; готовые результаты сохранены"
				default:
					m.state.Error = err.Error()
				}
			}

		}
		notify := m.notify
		m.mu.Unlock()
		if notify != nil {
			notify()
		}

		close(done)
	}()
	return nil
}
func (m *Manager) Cancel() {
	m.mu.Lock()
	cancel := m.jobCancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (m *Manager) transcribe(ctx context.Context, d *Document) error {
	for i := range d.Parts {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e := m.transcribePart(ctx, d, i); e != nil {
			return e
		}
		m.changed(func(s *State) { s.Completed = i + 1 })
	}
	return nil
}
func (m *Manager) transcribePart(ctx context.Context, d *Document, i int) error {
	p := d.Parts[i]
	pcm, e := m.openEvidence(*d, i)
	if e != nil {
		return e
	}
	defer pcm.close()
	signal, e := pcm.word(ctx, 0, float64(p.Samples)/float64(p.Rate))
	if e != nil {
		return e
	}
	if p.Transcribed {
		return m.filterSavedPart(ctx, d, p, pcm, signal)
	}
	if !signal {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		d.Parts[i].Transcribed = true
		d.Revision++
		return m.store.Save(*d)
	}
	path, offset, duration, remove, e := m.audioInput(*d, i)
	if e != nil {
		return e
	}
	words, e := m.processor.Transcribe(ctx, AudioInput{Path: path, Duration: duration})
	remove()
	if e != nil {
		return e
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	segments := []Segment{}
	for _, w := range words {
		if !finite(w.Start) || !finite(w.End) || w.Start < 0 || w.End < w.Start || w.End > duration+.01 {
			return errors.New("Недопустимые временные метки транскрипта")
		}
		start, end := offset+w.Start, offset+w.End
		mid := (start + end) / 2
		if mid < p.Offset || mid >= p.Offset+float64(p.Samples)/float64(p.Rate) {
			continue
		}
		start = max(start, p.Offset)
		end = min(end, p.Offset+float64(p.Samples)/float64(p.Rate))
		if strings.TrimSpace(w.Text) == "" {
			continue
		}

		signal, e := pcm.word(ctx, start-p.Offset, end-p.Offset)
		if e != nil {
			return e
		}
		if !signal {
			continue
		}
		if len(segments) > 0 {
			last := &segments[len(segments)-1]
			if last.Speaker == w.Speaker && start-last.End < 2 && len(last.Text)+len(w.Text) < 1200 {
				last.Text += " " + w.Text
				last.End = end
				continue
			}
		}
		segments = append(segments, Segment{ID: fmt.Sprintf("%s-%05d", p.File, len(segments)), Part: p.File, Track: p.Track, Speaker: w.Speaker, Start: start, End: end, Text: w.Text})
	}
	d.Segments = append(d.Segments, segments...)
	sort.SliceStable(d.Segments, func(i, j int) bool { return d.Segments[i].Start < d.Segments[j].Start })
	d.Parts[i].Transcribed = true
	d.Revision++
	if e = m.store.Save(*d); e != nil {
		return e
	}
	return nil
}

// audioInput adds up to two seconds from adjacent same-rate parts, preserving the original WAVs.
func (m *Manager) audioInput(d Document, index int) (string, float64, float64, func(), error) {
	p := d.Parts[index]
	var before, after *Part
	for i, q := range d.Parts {
		if i == index || q.Track != p.Track || q.Rate != p.Rate || !q.Complete {
			continue
		}
		if abs(q.Offset+float64(q.Samples)/float64(q.Rate)-p.Offset) < .001 {
			v := q
			before = &v
		}
		if abs(p.Offset+float64(p.Samples)/float64(p.Rate)-q.Offset) < .001 {
			v := q
			after = &v
		}
	}
	root, e := m.store.root(false)
	if e != nil {
		return "", 0, 0, func() {}, errArchive
	}
	defer func() { _ = root.Close() }()
	// Fixed application-owned temporary name, never a renderer path; one processing job at a time.
	name := d.ID + "/processing.wav"
	f, e := root.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
	if e != nil {
		return "", 0, 0, func() {}, errArchive
	}
	remove := func() {
		r, e := m.store.root(false)
		if e == nil {
			_ = r.Remove(name)
			_ = r.Close()
		}
	}
	var total int64
	offset := p.Offset
	if _, e = f.Write(wavHeader(p.Rate, 0)); e == nil {
		copyPart := func(part Part, start, n int64) error {
			src, e := m.store.OpenAudio(d.ID, part.File)
			if e != nil {
				return e
			}
			defer func() { _ = src.Close() }()
			copied, e := io.CopyN(f, io.NewSectionReader(src, 44+start*2, n*2), n*2)
			total += copied / 2
			return e
		}
		if before != nil {
			n := min(before.Samples, int64(p.Rate)*2)
			offset -= float64(n) / float64(p.Rate)
			e = copyPart(*before, before.Samples-n, n)
		}
		if e == nil {
			e = copyPart(p, 0, p.Samples)
		}
		if e == nil && after != nil {
			e = copyPart(*after, 0, min(after.Samples, int64(p.Rate)*2))
		}
	}
	if e == nil {
		_, e = f.WriteAt(wavHeader(p.Rate, total), 0)
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		remove()
		return "", 0, 0, func() {}, errors.New("Не удалось подготовить аудиочасть")
	}
	folder, e := m.store.Folder(d.ID)
	if e != nil {
		remove()
		return "", 0, 0, func() {}, e
	}
	return folder + string(os.PathSeparator) + "processing.wav", offset, float64(total) / float64(p.Rate), remove, nil
}
func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

const summaryBudget = 48000

func (m *Manager) summarize(ctx context.Context, d *Document, partial bool) error {
	if d.Leaves == nil {
		d.Leaves = map[string]Summary{}
	}
	summary, e := m.reduceSummary(ctx, d, d.Segments, 0)
	if e != nil {
		return e
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e = ValidateSummary(summary, d.Segments); e != nil {
		return e
	}
	summary.Partial = partial || d.Status != "recorded"
	summary.Revision = d.Revision
	summary.Model = "gemini-3.8-flash"
	d.Summary = &summary
	if e = m.store.Save(*d); e != nil {
		return e
	}
	m.changed(func(s *State) { s.Completed = s.Total })
	return nil
}
func (m *Manager) reduceSummary(ctx context.Context, d *Document, input []Segment, depth int) (Summary, error) {
	if depth > 8 {
		return Summary{}, errors.New("Summary не удаётся сократить без превышения лимита; промежуточные результаты сохранены")
	}
	groups := [][]Segment{}
	group := []Segment{}
	size := 2
	for _, seg := range input {
		b, _ := json.Marshal(seg)
		n := len(b) + 1
		if n > summaryBudget {
			return Summary{}, errors.New("Промежуточный итог превышает бюджет summary")
		}
		if size+n > summaryBudget && len(group) > 0 {
			groups = append(groups, group)
			group = nil
			size = 2
		}
		group = append(group, seg)
		size += n
	}
	if len(group) > 0 {
		groups = append(groups, group)
	}
	if len(groups) == 0 {
		return Summary{}, errors.New("Нет текста для summary")
	}
	m.changed(func(s *State) {
		if depth == 0 {
			s.Total = len(groups)
			s.Completed = 0
		} else {
			s.Total += len(groups)
		}
	})
	combined := []Segment{}
	for _, g := range groups {
		if ctx.Err() != nil {
			return Summary{}, ctx.Err()
		}
		b, _ := json.Marshal(g)
		hash := sha256.Sum256(b)
		key := hex.EncodeToString(hash[:])
		leaf, ok := d.Leaves[key]
		if !ok {
			var e error
			leaf, e = m.processor.Summarize(ctx, g)
			if e != nil {
				return Summary{}, e
			}
			if e = ValidateSummary(leaf, g); e != nil {
				return Summary{}, e
			}
			d.Leaves[key] = leaf
			if e = m.store.Save(*d); e != nil {
				return Summary{}, e
			}
		}
		m.changed(func(s *State) { s.Completed++ })
		if len(groups) == 1 {
			return leaf, nil
		}
		text, _ := json.Marshal(leaf)
		// Only cited original IDs accompany an intermediate result, rather than every word/segment.
		first := g[0].ID
		combined = append(combined, Segment{ID: first, Text: string(text)})
		seen := map[string]bool{first: true}
		for _, items := range [][]Item{leaf.Decisions, leaf.Tasks} {
			for _, item := range items {
				for _, id := range item.Sources {
					if !seen[id] {
						seen[id] = true
						combined = append(combined, Segment{ID: id, Text: "Основание решения или задачи из промежуточного итога"})
					}
				}
			}
		}
	}
	return m.reduceSummary(ctx, d, combined, depth+1)
}
