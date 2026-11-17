package meeting

import (
	"context"
	"testing"
	"time"
)

type rateLimitedProcessor struct {
	fakeProcessor
	calls   int
	blocked bool
	delay   time.Duration
}

func (p *rateLimitedProcessor) Transcribe(context.Context, AudioInput) ([]Word, error) {
	p.calls++
	if p.blocked && p.calls >= 2 {
		return nil, &RetryableError{RetryAfter: p.delay}
	}
	return []Word{{Text: "речь", Start: .94, End: .98}}, nil
}
func (p *rateLimitedProcessor) Summarize(ctx context.Context, s []Segment) (Summary, error) {
	if p.blocked {
		return Summary{}, &RetryableError{RetryAfter: p.delay}
	}
	return p.fakeProcessor.Summarize(ctx, s)
}
func TestRateLimitPausesAndResumesOnlyUnfinishedParts(t *testing.T) {
	store := NewStore(t.TempDir())
	d := boundaryRecording(t, store)
	p := &rateLimitedProcessor{blocked: true}
	m := NewManager(store, nil, p)
	if e := m.Process(context.Background(), d.ID, "transcribe", false); e != nil {
		t.Fatal(e)
	}
	waitJob(t, m)
	s := m.State()
	if s.Error != "" || s.Paused == nil || s.Paused.ID != d.ID || s.Paused.Kind != "transcribe" {
		t.Fatalf("not paused: %+v", s)
	}
	got, e := m.Load(d.ID)
	if e != nil || !got.Parts[0].Transcribed || got.Parts[1].Transcribed {
		t.Fatalf("checkpoint lost %+v %v", got.Parts, e)
	}
	p.blocked = false
	if e = m.Process(context.Background(), s.Paused.ID, s.Paused.Kind, s.Paused.Partial); e != nil {
		t.Fatal(e)
	}
	waitJob(t, m)
	if p.calls != 3 || m.State().Paused != nil || m.State().Error != "" {
		t.Fatalf("bad continuation calls=%d state=%+v", p.calls, m.State())
	}
}
func TestRateLimitCooldownAndSnapshotOwnership(t *testing.T) {
	store := NewStore(t.TempDir())
	d := boundaryRecording(t, store)
	p := &rateLimitedProcessor{blocked: true, delay: time.Minute}
	m := NewManager(store, nil, p)
	if e := m.Process(context.Background(), d.ID, "transcribe", false); e != nil {
		t.Fatal(e)
	}
	waitJob(t, m)
	s := m.State()
	if s.Paused == nil || s.Paused.RetryAt == "" {
		t.Fatal("retry time missing")
	}
	s.Paused.ID = "changed"
	if m.State().Paused.ID != d.ID {
		t.Fatal("snapshot shares mutable paused state")
	}
	if e := m.Process(context.Background(), d.ID, "transcribe", false); e == nil || p.calls != 2 {
		t.Fatal("cooldown bypassed")
	}
}
func TestRateLimitKeepsPartialSummaryChoice(t *testing.T) {
	store := NewStore(t.TempDir())
	d := recordedPart(t, store, []float32{.01})
	d.Status = "interrupted"
	d.Parts[0].Transcribed = true
	d.Segments = []Segment{{ID: "source", Part: d.Parts[0].File, Track: "mic", Start: 0, End: .08, Text: "Речь"}}
	if e := store.Save(d); e != nil {
		t.Fatal(e)
	}
	p := &rateLimitedProcessor{blocked: true}
	m := NewManager(store, nil, p)
	if e := m.Process(context.Background(), d.ID, "summary", true); e != nil {
		t.Fatal(e)
	}
	waitJob(t, m)
	s := m.State()
	if s.Paused == nil || !s.Paused.Partial || s.Paused.Kind != "summary" || s.Error != "" {
		t.Fatalf("partial pause lost: %+v", s)
	}
	p.blocked = false
	if e := m.Process(context.Background(), s.Paused.ID, s.Paused.Kind, s.Paused.Partial); e != nil {
		t.Fatal(e)
	}
	waitJob(t, m)
	got, e := m.Load(d.ID)
	if e != nil || got.Summary == nil || !got.Summary.Partial {
		t.Fatalf("partial summary lost: %+v %v", got.Summary, e)
	}
}

type unavailableSummaryProcessor struct {
	fakeProcessor
	unavailable bool
}

func (p *unavailableSummaryProcessor) Summarize(ctx context.Context, s []Segment) (Summary, error) {
	if p.unavailable {
		return Summary{}, &RetryableError{StatusCode: 503}
	}
	return p.fakeProcessor.Summarize(ctx, s)
}
func TestUnavailableSummaryCanContinue(t *testing.T) {
	store := NewStore(t.TempDir())
	d := recordedPart(t, store, []float32{.01})
	d.Parts[0].Transcribed = true
	d.Segments = []Segment{{ID: "source", Part: d.Parts[0].File, Track: "mic", Start: 0, End: .08, Text: "Речь"}}
	if e := store.Save(d); e != nil {
		t.Fatal(e)
	}
	p := &unavailableSummaryProcessor{unavailable: true}
	m := NewManager(store, nil, p)
	if e := m.Process(context.Background(), d.ID, "summary", false); e != nil {
		t.Fatal(e)
	}
	waitJob(t, m)
	state := m.State()
	if state.Paused == nil || state.Error != "" || state.Paused.Kind != "summary" {
		t.Fatalf("503 not resumable: %+v", state)
	}
	p.unavailable = false
	if e := m.Process(context.Background(), state.Paused.ID, state.Paused.Kind, state.Paused.Partial); e != nil {
		t.Fatal(e)
	}
	waitJob(t, m)
	got, e := m.Load(d.ID)
	if e != nil || got.Summary == nil || len(got.Segments) != 1 {
		t.Fatalf("result lost: %+v %v", got, e)
	}
}
