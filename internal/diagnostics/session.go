// Package diagnostics keeps a bounded, local log of each application session.
package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DefaultKeep     = 20
	DefaultMaxBytes = 8 << 20
	// errorReserve lets errors through after the size limit has been reached.
	errorReserve = 1 << 20
	queueSize    = 1024
	stampLayout  = "20060102-150405"
	endMessage   = "session end"
)

type Options struct {
	Dir      string
	Now      func() time.Time
	Keep     int
	MaxBytes int64
	// CrashOutput routes fatal Go panics of the whole process to the session's crash file.
	CrashOutput bool
	Level       slog.Leveler
}

// Session owns one log file. Logging never blocks the caller: records that do
// not fit the queue are counted and reported by the writer later.
type Session struct {
	Logger *slog.Logger
	// Name is the shared "<UTC stamp>-<pid>" part of the session's file names.
	Name string
	// PreviousCrash is the crash file left non-empty by the previous session.
	PreviousCrash string
	// PreviousUnclean is set when the previous session log has no end record.
	PreviousUnclean bool

	dir       string
	file      *os.File
	crashPath string
	queue     chan []byte
	flushes   chan chan struct{}
	stop      chan struct{}
	done      chan struct{}
	dropped   atomic.Int64
	written   atomic.Int64
	limited   atomic.Bool
	maxBytes  int64
	closeOnce sync.Once
	closeErr  error
	now       func() time.Time
	started   time.Time
}

func sessionLog(name string) string   { return "session-" + name + ".log" }
func sessionCrash(name string) string { return "crash-" + name + ".txt" }

func Open(o Options) (*Session, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Keep <= 0 {
		o.Keep = DefaultKeep
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.Level == nil {
		o.Level = slog.LevelInfo
	}
	if err := os.MkdirAll(o.Dir, 0700); err != nil {
		return nil, err
	}
	started := o.Now().UTC()
	name := fmt.Sprintf("%s-%d", started.Format(stampLayout), os.Getpid())
	previous, err := prune(o.Dir, o.Keep-1)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(o.Dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.OpenFile(sessionLog(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	s := &Session{
		Name: name, dir: o.Dir, file: file, maxBytes: o.MaxBytes, now: o.Now, started: started,
		queue: make(chan []byte, queueSize), flushes: make(chan chan struct{}), stop: make(chan struct{}), done: make(chan struct{}),
	}
	if previous != "" {
		s.PreviousCrash, s.PreviousUnclean = inspectPrevious(root, previous)
	}
	if o.CrashOutput {
		crash, err := root.OpenFile(sessionCrash(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			// SetCrashOutput duplicates the descriptor; ours can be closed at once.
			err = debug.SetCrashOutput(crash, debug.CrashOptions{})
			_ = crash.Close()
			s.crashPath = filepath.Join(o.Dir, sessionCrash(name))
		}
		if err != nil {
			_ = file.Close()
			_ = root.Remove(sessionLog(name))
			return nil, err
		}
	}
	s.Logger = slog.New(&handler{session: s, inner: slog.NewTextHandler(writer{s}, &slog.HandlerOptions{Level: o.Level, ReplaceAttr: replaceAttr})})
	go s.run()
	return s, nil
}

// Dir is the folder holding all session logs.
func (s *Session) Dir() string { return s.dir }

// Path is the current session log file.
func (s *Session) Path() string { return filepath.Join(s.dir, sessionLog(s.Name)) }

// Flush waits until queued records are written, at most for timeout.
func (s *Session) Flush(timeout time.Duration) {
	reply := make(chan struct{})
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case s.flushes <- reply:
	case <-s.done:
		return
	case <-timer.C:
		return
	}
	select {
	case <-reply:
	case <-timer.C:
	}
}

// Close writes the end record, drains the queue and removes an unused crash file.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		s.Logger.Info(endMessage, "uptime", s.now().UTC().Sub(s.started).Round(time.Millisecond).String())
		close(s.stop)
		<-s.done
		s.closeErr = errors.Join(s.file.Sync(), s.file.Close())
		if s.crashPath != "" {
			if info, err := os.Lstat(s.crashPath); err == nil && info.Mode().IsRegular() && info.Size() == 0 {
				// The process may still panic after Close; keep routing crashes to the file then.
				_ = os.Remove(s.crashPath)
			}
		}
	})
	return s.closeErr
}

func (s *Session) run() {
	defer close(s.done)
	write := func(b []byte) {
		if n, err := s.file.Write(b); err == nil {
			s.written.Add(int64(n))
		}
		if n := s.dropped.Swap(0); n > 0 {
			line := fmt.Sprintf("time=%s level=WARN msg=\"log records dropped\" count=%d\n", s.now().UTC().Format(time.RFC3339Nano), n)
			if m, err := s.file.WriteString(line); err == nil {
				s.written.Add(int64(m))
			}
		}
	}
	drain := func() {
		for {
			select {
			case b := <-s.queue:
				write(b)
			default:
				return
			}
		}
	}
	for {
		select {
		case b := <-s.queue:
			write(b)
		case reply := <-s.flushes:
			drain()
			close(reply)
		case <-s.stop:
			drain()
			return
		}
	}
}

type writer struct{ s *Session }

func (w writer) Write(p []byte) (int, error) {
	b := append([]byte(nil), p...)
	select {
	case w.s.queue <- b:
	default:
		w.s.dropped.Add(1)
	}
	return len(p), nil
}

// handler bounds the file size: after MaxBytes only errors pass, and one
// marker record says so; after the reserve nothing is written.
type handler struct {
	session *Session
	inner   slog.Handler
}

func (h *handler) Enabled(ctx context.Context, l slog.Level) bool { return h.inner.Enabled(ctx, l) }
func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	s := h.session
	select {
	case <-s.stop:
		return nil
	default:
	}
	used := s.written.Load() + int64(len(s.queue))*256
	if r.Message == endMessage {
		// The end record marks a clean exit and is written even past the limit.
		return h.inner.Handle(ctx, r)
	}
	if used >= s.maxBytes+errorReserve {
		s.dropped.Add(1)
		return nil
	}
	if used >= s.maxBytes && r.Level < slog.LevelError {
		if s.limited.CompareAndSwap(false, true) {
			marker := slog.NewRecord(r.Time, slog.LevelWarn, "log size limit reached; only errors are written", 0)
			marker.AddAttrs(slog.Int64("limit_bytes", s.maxBytes))
			_ = h.inner.Handle(ctx, marker)
		}
		s.dropped.Add(1)
		return nil
	}
	return h.inner.Handle(ctx, r)
}
func (h *handler) WithAttrs(a []slog.Attr) slog.Handler {
	return &handler{session: h.session, inner: h.inner.WithAttrs(a)}
}
func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{session: h.session, inner: h.inner.WithGroup(name)}
}

func replaceAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindTime:
		a.Value = slog.TimeValue(a.Value.Time().UTC())
	case slog.KindString:
		a.Value = slog.StringValue(Redact(a.Value.String()))
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			a.Value = slog.StringValue(Redact(err.Error()))
		}
	default:
	}
	return a
}
