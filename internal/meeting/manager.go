package meeting

import (
	"context"
	"errors"
	"sync"

	"github.com/97Dmitry/solveo/internal/audio"
)

type Manager struct {
	store     *Store
	capture   audio.Capturer
	processor Processor
	opMu      sync.Mutex
	workers   sync.WaitGroup
	mu        sync.Mutex
	state     State
	recording *recording
	jobCancel context.CancelFunc
	jobDone   chan struct{}
	notify    func()
	closed    bool
}

func NewManager(store *Store, capture audio.Capturer, processor Processor) *Manager {
	return &Manager{store: store, capture: capture, processor: processor}
}
func (m *Manager) SetNotify(fn func()) { m.mu.Lock(); m.notify = fn; m.mu.Unlock() }
func (m *Manager) changed(fn func(*State)) {
	m.mu.Lock()
	fn(&m.state)
	m.state.Revision++
	notify := m.notify
	m.mu.Unlock()
	if notify != nil {
		notify()
	}
}
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.state
	if s.Paused != nil {
		paused := *s.Paused
		s.Paused = &paused
	}
	return s
}
func (m *Manager) List() ([]Info, error) {
	list, e := m.store.List()
	state := m.State()
	for i := range list {
		if list[i].ID == state.RecordingID {
			list[i].Status = "recording"
		}
	}
	return list, e
}
func (m *Manager) Load(id string) (Document, error) { return m.store.Load(id) }
func (m *Manager) Start(ctx context.Context, title, chatID string, tracks []string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	busy := m.recording != nil || m.closed
	m.mu.Unlock()
	if busy {
		return errors.New("Запись уже идёт или приложение завершается")
	}
	if m.capture == nil {
		return errors.New("Захват звука недоступен")
	}
	d, e := m.store.New(title, chatID, tracks)
	if e != nil {
		return e
	}
	r := newRecording(ctx, m.store, d)
	m.mu.Lock()
	m.recording = r
	m.mu.Unlock()
	for _, track := range tracks {
		if e = r.addSource(m.capture, audio.Source(track)); e != nil {
			r.fail("Не удалось открыть источник " + track)
			break
		}
	}
	m.changed(func(s *State) { s.RecordingID = d.ID; s.Error = "" })
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		<-r.ctx.Done()
		err := r.finish()
		m.mu.Lock()
		if m.recording == r {
			m.recording = nil
			m.state.RecordingID = ""
			if err != nil {
				m.state.Error = err.Error()
			}
			m.state.Revision++
		}
		notify := m.notify
		m.mu.Unlock()
		if notify != nil {
			notify()
		}
		close(r.done)
	}()
	if e != nil {
		r.cancel()
		<-r.done
	}
	return e
}
func (m *Manager) Stop() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	r := m.recording
	m.mu.Unlock()
	if r == nil {
		return nil
	}
	r.cancel()
	<-r.done
	return r.result
}
func (m *Manager) Recover(id string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if e := m.mutable(id); e != nil {
		return e
	}
	_, e := m.store.Recover(id)
	m.changed(func(*State) {})
	return e
}
func (m *Manager) mutable(id string) error {
	s := m.State()
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed || s.RecordingID == id || s.ProcessingID == id {
		return errors.New("Встреча занята")
	}
	return nil
}
func (m *Manager) Rename(id, title string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if !validTitle(title) {
		return errors.New("Название должно содержать от 1 до 200 символов")
	}
	if e := m.mutable(id); e != nil {
		return e
	}
	d, e := m.store.Load(id)
	if e != nil {
		return e
	}
	d.Title = title
	e = m.store.Save(d)
	m.changed(func(*State) {})
	return e
}
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed = true
	cancel, done := m.jobCancel, m.jobDone
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	e := m.Stop()
	if done != nil {
		<-done
	}
	m.workers.Wait()
	return e
}
func (m *Manager) Reopen() { m.mu.Lock(); m.closed = false; m.mu.Unlock() }
