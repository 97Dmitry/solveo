package meeting

import (
	"context"
	"errors"
	"testing"
)

type cleanupFailureProcessor struct {
	fakeProcessor
	primaryErr error
}

func (p *cleanupFailureProcessor) Transcribe(ctx context.Context, in AudioInput) ([]Word, error) {
	if p.primaryErr != nil {
		return nil, p.primaryErr
	}
	return p.fakeProcessor.Transcribe(ctx, in)
}
func (*cleanupFailureProcessor) Cleanup(context.Context) error { return context.DeadlineExceeded }
func TestCleanupFailureDoesNotFailSavedTranscription(t *testing.T) {
	store := NewStore(t.TempDir())
	d := recordedPart(t, store, []float32{.01, .01, .01, .01})
	p := &cleanupFailureProcessor{}
	m := NewManager(store, nil, p)
	if e := m.Process(context.Background(), d.ID, "transcribe", false); e != nil {
		t.Fatal(e)
	}
	waitJob(t, m)
	got, e := m.Load(d.ID)
	if e != nil || !got.Parts[0].Transcribed || len(got.Segments) == 0 {
		t.Fatalf("transcript not saved: %+v %v", got, e)
	}
	s := m.State()
	if s.Error != "" {
		t.Fatalf("cleanup turned success into error: %s", s.Error)
	}
	if s.Notice == "" {
		t.Fatal("cleanup warning missing")
	}
}
func TestCleanupFailurePreservesPrimaryError(t *testing.T) {
	store := NewStore(t.TempDir())
	d := recordedPart(t, store, []float32{.01, .01})
	p := &cleanupFailureProcessor{primaryErr: errors.New("recognition failed")}
	m := NewManager(store, nil, p)
	if e := m.Process(context.Background(), d.ID, "transcribe", false); e != nil {
		t.Fatal(e)
	}
	waitJob(t, m)
	if m.State().Error != "recognition failed" {
		t.Fatalf("primary error lost: %s", m.State().Error)
	}
}
