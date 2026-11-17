package meeting

import (
	"context"
	"errors"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/97Dmitry/solveo/internal/audio"
	"github.com/97Dmitry/solveo/internal/chat"
)

type recording struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	store   *Store
	mu      sync.Mutex
	doc     Document
	started time.Time
	endedAt time.Time
	stops   []func()
	queues  []chan audio.Frame
	writers sync.WaitGroup
	failed  atomic.Bool
	failure chan string
	result  error
}

func newRecording(parent context.Context, s *Store, d Document) *recording {
	ctx, cancel := context.WithCancel(parent)
	return &recording{ctx: ctx, cancel: cancel, done: make(chan struct{}), store: s, doc: d, started: time.Now(), failure: make(chan string, 1)}
}
func (r *recording) fail(message string) {
	r.failed.Store(true)
	select {
	case r.failure <- message:
	default:
	}
	r.cancel()
}

func (r *recording) addSource(capture audio.Capturer, source audio.Source) error {
	q := make(chan audio.Frame, 100)
	r.queues = append(r.queues, q)
	offset := time.Since(r.started).Seconds()
	r.writers.Go(func() { r.writeSource(string(source), offset, q) })
	_, stop, e := capture.Start(r.ctx, source, func(f audio.Frame) {
		select {
		case q <- f:
		default:
			r.fail("Диск не успевает сохранять звук; запись остановлена")
		}
	}, func(error) {
		r.fail("Устройство звука отключено; запись прервана")
	})
	if e == nil {
		r.stops = append(r.stops, stop)
	}
	return e
}
func (r *recording) writeSource(track string, offset float64, q <-chan audio.Frame) {
	var w *wavWriter
	index := 0
	partIndex := -1
	var elapsed int64
	var rate int
	failed := false
	finish := func() bool {
		if w == nil {
			return true
		}
		e := w.close()
		r.mu.Lock()
		r.doc.Parts[partIndex].Samples = w.samples
		r.doc.Parts[partIndex].Complete = e == nil
		measure(&r.doc)
		saveErr := r.store.Save(r.doc)
		r.mu.Unlock()
		w = nil
		if e != nil || saveErr != nil {
			r.fail("Не удалось завершить запись звука")
			return false
		}
		return true
	}
	defer func() { _ = finish() }()

	var firstAt time.Time
	write := func(samples []float32, channels int) {

		for len(samples) > 0 {
			if w == nil {
				file := partName(track, index)
				index++
				r.mu.Lock()
				r.doc.Parts = append(r.doc.Parts, Part{File: file, Track: track, Rate: rate, Offset: offset + float64(elapsed)/float64(rate)})
				partIndex = len(r.doc.Parts) - 1
				e := r.store.Save(r.doc)
				r.mu.Unlock()
				if e != nil {
					r.fail("Не удалось сохранить метаданные записи")
					failed = true
					break
				}
				root, e := r.store.root(false)
				if e != nil {
					r.fail("Архив записи недоступен")
					failed = true
					break
				}
				fileHandle, e := root.OpenFile(r.doc.ID+"/"+file, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
				_ = root.Close()
				if e == nil {
					w = &wavWriter{file: fileHandle, rate: rate}
					_, e = fileHandle.Write(wavHeader(rate, 0))
				}
				if e != nil {
					if fileHandle != nil {
						_ = fileHandle.Close()
					}
					w = nil
					r.fail("Не удалось создать аудиофайл")
					failed = true
					break
				}
			}
			n := min(len(samples)/channels, int(int64(rate)*PartSeconds-w.samples))
			if e := w.write(samples[:n*channels], channels); e != nil {
				r.fail("Ошибка записи звука на диск")
				failed = true
				break
			}
			elapsed += int64(n)
			samples = samples[n*channels:]
			r.mu.Lock()
			r.doc.Parts[partIndex].Samples = w.samples
			measure(&r.doc)
			limit := r.doc.Bytes > MaxBytes || r.doc.Duration > MaxSeconds
			r.mu.Unlock()
			if limit {
				r.fail("Достигнут предел записи: 8 часов или 8 GiB")
				failed = true
				break
			}
			if w.samples == int64(rate)*PartSeconds {
				if !finish() {
					failed = true
					break
				}
			}
		}
	}
	zeros := func(n int64) {
		for n > 0 && !failed {
			count := min(n, int64(rate)/10)
			write(make([]float32, int(count)), 1)
			n -= count
		}
	}
	for f := range q {
		if failed {
			continue
		}
		if f.Rate < 8000 || f.Rate > 192000 || f.Channels < 1 || f.Channels > 32 || len(f.Samples)%f.Channels != 0 || (rate != 0 && f.Rate != rate) {
			r.fail("Изменился или не поддерживается формат устройства")
			failed = true
			continue
		}
		rate = f.Rate
		if f.Discontinuity {
			r.fail("Устройство сообщило о потере аудио; запись прервана")
			failed = true
			continue
		}
		if firstAt.IsZero() && !f.At.IsZero() {
			firstAt = f.At
			offset = max(0, f.At.Sub(r.started).Seconds())
		}
		if f.Synthetic {
			// Keep a short unwritten tail so a newly delivered endpoint packet can replace silence.
			target := int64(math.Round((f.At.Sub(firstAt).Seconds() - .25) * float64(rate)))
			if target > elapsed {
				zeros(target - elapsed)
			}
			continue
		}
		samples := f.Samples
		if !firstAt.IsZero() && !f.At.IsZero() {
			expected := int64(math.Round(f.At.Sub(firstAt).Seconds() * float64(rate)))
			delta := expected - elapsed
			// Ignore sub-2ms clock jitter; material gaps keep their duration as silence.
			if delta > int64(rate)/500 {
				zeros(delta)
			} else if delta < -int64(rate)/500 {
				trim := min(int64(len(samples)/f.Channels), -delta)
				samples = samples[int(trim)*f.Channels:]
			}
		}
		write(samples, f.Channels)
	}
	if !firstAt.IsZero() && !failed && !r.failed.Load() {
		target := int64(math.Round(r.endedAt.Sub(firstAt).Seconds() * float64(rate)))
		if target > elapsed {
			zeros(target - elapsed)
		}
	}

}
func (r *recording) finish() error {
	r.endedAt = time.Now()
	for _, stop := range r.stops {
		stop()
	}
	for _, q := range r.queues {
		close(q)
	}
	r.writers.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.doc.Status = "recorded"
	var failure string
	select {
	case failure = <-r.failure:
	default:
	}
	if failure != "" {
		r.doc.Status = "interrupted"
		r.doc.Error = failure
	}
	r.doc.EndedAt = chat.Now()
	r.doc.Revision++
	measure(&r.doc)
	err := r.store.Save(r.doc)
	if failure != "" {
		err = errors.Join(errors.New(failure), err)
	}
	r.result = err
	return err
}
