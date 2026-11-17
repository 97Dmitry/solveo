package meeting

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/audio"
)

type syntheticCapture struct {
	mu    sync.Mutex
	sinks map[audio.Source]func(audio.Frame)
	fail  func(error)
}

func (f *syntheticCapture) Start(_ context.Context, s audio.Source, sink func(audio.Frame), fail func(error)) (audio.Device, func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sinks == nil {
		f.sinks = map[audio.Source]func(audio.Frame){}
	}
	f.sinks[s] = sink
	f.fail = fail
	return audio.Device{Name: "test"}, func() { f.mu.Lock(); delete(f.sinks, s); f.mu.Unlock() }, nil
}
func (f *syntheticCapture) push(s audio.Source, frame audio.Frame) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sink := f.sinks[s]; sink != nil {
		sink(frame)
	}
}
func TestRecordingRotationAndRestart(t *testing.T) {
	s := NewStore(t.TempDir())
	capture := &syntheticCapture{}
	m := NewManager(s, capture, nil)
	if e := m.Start(context.Background(), "Meeting", "", []string{"system"}); e != nil {
		t.Fatal(e)
	}
	id := m.State().RecordingID
	// A large synthetic frame crosses a part boundary without restarting capture.
	capture.push(audio.System, audio.Frame{Rate: 8000, Channels: 1, Samples: make([]float32, 8000*(PartSeconds+1))})
	if e := m.Stop(); e != nil {
		t.Fatal(e)
	}
	d, e := m.Load(id)
	if e != nil {
		t.Fatal(e)
	}
	if len(d.Parts) != 2 || d.Parts[0].Samples != 8000*PartSeconds || d.Parts[1].Samples != 8000 || d.Status != "recorded" {
		t.Fatalf("%+v", d)
	}
	if e = m.Start(context.Background(), "Next", "", []string{"mic"}); e != nil {
		t.Fatal(e)
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
}
func TestCaptureFailureStopsAndPreserves(t *testing.T) {
	capture := &syntheticCapture{}
	m := NewManager(NewStore(t.TempDir()), capture, nil)
	if e := m.Start(context.Background(), "Meeting", "", []string{"mic"}); e != nil {
		t.Fatal(e)
	}
	id := m.State().RecordingID
	capture.push(audio.Mic, audio.Frame{Rate: 16000, Channels: 1, Samples: make([]float32, 1600)})
	capture.fail(errors.New("device lost"))
	deadline := time.Now().Add(3 * time.Second)
	for m.State().RecordingID != "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	d, e := m.Load(id)
	if e != nil {
		t.Fatal(e)
	}
	if d.Status != "interrupted" || len(d.Parts) != 1 || d.Parts[0].Samples != 1600 {
		t.Fatalf("%+v", d)
	}
}

func TestRecordingPreservesPacketGap(t *testing.T) {
	capture := &syntheticCapture{}
	m := NewManager(NewStore(t.TempDir()), capture, nil)
	if e := m.Start(context.Background(), "Meeting", "", []string{"mic"}); e != nil {
		t.Fatal(e)
	}
	id := m.State().RecordingID
	at := time.Now()
	capture.push(audio.Mic, audio.Frame{At: at, Rate: 16000, Channels: 1, Samples: make([]float32, 1600)})
	capture.push(audio.Mic, audio.Frame{At: at.Add(time.Second), Rate: 16000, Channels: 1, Samples: make([]float32, 1600)})
	if e := m.Stop(); e != nil {
		t.Fatal(e)
	}
	d, e := m.Load(id)
	if e != nil {
		t.Fatal(e)
	}
	if d.Parts[0].Samples != 17600 {
		t.Fatalf("gap collapsed: %d", d.Parts[0].Samples)
	}
}

func TestOverflowCallbackDoesNotWaitForDisk(t *testing.T) {
	capture := &syntheticCapture{}
	store := NewStore(t.TempDir())
	m := NewManager(store, capture, nil)
	if e := m.Start(context.Background(), "Meeting", "", []string{"mic"}); e != nil {
		t.Fatal(e)
	}
	store.mu.Lock()
	capture.push(audio.Mic, audio.Frame{Rate: 16000, Channels: 1, Samples: make([]float32, 16)})
	m.mu.Lock()
	r := m.recording
	m.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for r.mu.TryLock() {
		r.mu.Unlock()
		if time.Now().After(deadline) {
			store.mu.Unlock()
			t.Fatal("writer did not reach disk")
		}
		time.Sleep(time.Millisecond)
	}
	done := make(chan struct{})
	go func() {
		for range 110 {
			capture.push(audio.Mic, audio.Frame{Rate: 16000, Channels: 1, Samples: make([]float32, 16)})
		}
		close(done)
	}()
	select {
	case <-done:
		store.mu.Unlock()
	case <-time.After(time.Second):
		store.mu.Unlock()
		<-done
		t.Fatal("capture callback waited for disk mutex")
	}
	_ = m.Stop()
}

func TestCloseWaitsForCompletionNotification(t *testing.T) {
	capture := &syntheticCapture{}
	m := NewManager(NewStore(t.TempDir()), capture, nil)
	if e := m.Start(context.Background(), "Meeting", "", []string{"mic"}); e != nil {
		t.Fatal(e)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	m.SetNotify(func() {
		if m.State().RecordingID == "" {
			close(entered)
			<-release
		}
	})
	capture.fail(errors.New("device lost"))
	<-entered
	closed := make(chan struct{})
	go func() { _ = m.Close(); close(closed) }()
	select {
	case <-closed:
		close(release)
		t.Fatal("Close returned while recorder completion was still running")
	case <-time.After(30 * time.Millisecond):
		close(release)
	}
	<-closed
}
