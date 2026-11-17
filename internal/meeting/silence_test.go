package meeting

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// recordedPart creates real canonical WAV bytes; provider results are fake, never a real request.
func recordedPart(t *testing.T, store *Store, samples []float32) Document {
	t.Helper()
	d, e := store.New("Meeting", "", []string{"mic"})
	if e != nil {
		t.Fatal(e)
	}
	w, e := newWAV(filepath.Join(store.dir, d.ID, "mic-0000.wav"), 16000)
	if e != nil {
		t.Fatal(e)
	}
	// Repeat short signals to cover the fake provider's word intervals.
	if len(samples) < 1600 {
		original := samples
		samples = make([]float32, 1600)
		for i := range samples {
			samples[i] = original[i%len(original)]
		}
	}
	if e = w.write(samples, 1); e != nil {
		t.Fatal(e)
	}
	if e = w.close(); e != nil {
		t.Fatal(e)
	}
	d.Status = "recorded"
	d.Parts = []Part{{File: "mic-0000.wav", Track: "mic", Rate: 16000, Samples: int64(len(samples)), Complete: true}}
	measure(&d)
	if e = store.Save(d); e != nil {
		t.Fatal(e)
	}
	return d
}
func TestSilentPartSkipsProvider(t *testing.T) {
	store := NewStore(t.TempDir())
	d := recordedPart(t, store, make([]float32, 1600))
	p := &fakeProcessor{}
	m := NewManager(store, nil, p)
	if e := m.transcribe(context.Background(), &d); e != nil {
		t.Fatal(e)
	}
	if p.calls != 0 || len(d.Segments) != 0 || !d.Parts[0].Transcribed {
		t.Fatalf("silence sent or hallucination saved: calls=%d segments=%+v", p.calls, d.Segments)
	}
}

type wordProcessor struct {
	fakeProcessor
	words []Word
}

func (p *wordProcessor) Transcribe(context.Context, AudioInput) ([]Word, error) {
	p.calls++
	return p.words, nil
}
func TestSilenceFilterKeepsQuietSpeechAndTimestampPadding(t *testing.T) {
	for _, amplitude := range []float32{1.0 / 32768, -1.0 / 32768} {
		store := NewStore(t.TempDir())
		samples := make([]float32, 32000)
		samples[8000] = amplitude
		d := recordedPart(t, store, samples)
		p := &wordProcessor{words: []Word{{Text: "тихо", Start: .7, End: .71}, {Text: "20", Start: 1.4, End: 1.5}}}
		m := NewManager(store, nil, p)
		if e := m.transcribe(context.Background(), &d); e != nil {
			t.Fatal(e)
		}
		if len(d.Segments) != 1 || d.Segments[0].Text != "тихо" {
			t.Fatalf("quiet signal removed or silence retained: %+v", d.Segments)
		}
	}
}
func TestRepeatFiltersSavedSegmentsWithoutProvider(t *testing.T) {
	store := NewStore(t.TempDir())
	samples := make([]float32, 32000)
	samples[1600] = .1
	d := recordedPart(t, store, samples)
	d.Parts[0].Transcribed = true
	d.Segments = []Segment{{ID: "real", Part: d.Parts[0].File, Track: "mic", Start: .1, End: .15, Text: "Речь"}, {ID: "false", Part: d.Parts[0].File, Track: "mic", Start: 1.2, End: 1.4, Text: "20"}}
	d.Summary = &Summary{Overview: "Старый итог", Revision: d.Revision}
	d.Leaves = map[string]Summary{"old": {Overview: "cache"}}
	if e := store.Save(d); e != nil {
		t.Fatal(e)
	}
	p := &fakeProcessor{}
	m := NewManager(store, nil, p)
	if e := m.transcribe(context.Background(), &d); e != nil {
		t.Fatal(e)
	}
	got, e := store.Load(d.ID)
	if e != nil {
		t.Fatal(e)
	}
	if len(got.Segments) != 1 || got.Segments[0].ID != "real" || p.calls != 0 || got.Summary.Revision == got.Revision || len(got.Leaves) != 0 {
		t.Fatalf("unsafe recheck %+v calls=%d", got, p.calls)
	}
	revision := got.Revision
	if e = m.transcribe(context.Background(), &got); e != nil {
		t.Fatal(e)
	}
	if got.Revision != revision || p.calls != 0 {
		t.Fatal("recheck is not idempotent")
	}
}
func TestMissingAudioIsNotSilence(t *testing.T) {
	store := NewStore(t.TempDir())
	d := recordedPart(t, store, []float32{.1})
	if e := os.Remove(filepath.Join(store.dir, d.ID, d.Parts[0].File)); e != nil {
		t.Fatal(e)
	}
	p := &fakeProcessor{}
	m := NewManager(store, nil, p)
	if e := m.transcribe(context.Background(), &d); e == nil {
		t.Fatal("missing audio treated as silence")
	}
	if d.Parts[0].Transcribed || p.calls != 0 {
		t.Fatal("failed read accepted")
	}
}

func boundaryRecording(t *testing.T, store *Store) Document {
	t.Helper()
	samples := make([]float32, 16000)
	for i := 15200; i < 16000; i++ {
		samples[i] = .01
	}
	d := recordedPart(t, store, samples)
	w, e := newWAV(filepath.Join(store.dir, d.ID, "mic-0001.wav"), 16000)
	if e != nil {
		t.Fatal(e)
	}
	if e = w.write(make([]float32, 16000), 1); e != nil {
		t.Fatal(e)
	}
	if e = w.close(); e != nil {
		t.Fatal(e)
	}
	d.Parts = append(d.Parts, Part{File: "mic-0001.wav", Track: "mic", Rate: 16000, Offset: 1, Samples: 16000, Complete: true})
	measure(&d)
	if e = store.Save(d); e != nil {
		t.Fatal(e)
	}
	return d
}
func TestBoundaryWordUsesAdjacentPartEvidence(t *testing.T) {
	store := NewStore(t.TempDir())
	d := boundaryRecording(t, store)
	p := &wordProcessor{words: []Word{{Text: "граница", Start: .98, End: 1.04}}}
	m := NewManager(store, nil, p)
	if e := m.transcribe(context.Background(), &d); e != nil {
		t.Fatal(e)
	}
	if p.calls != 2 || len(d.Segments) != 1 || d.Segments[0].Part != "mic-0001.wav" {
		t.Fatalf("boundary speech lost: calls=%d segments=%+v", p.calls, d.Segments)
	}
}
func TestSavedBoundaryWordSurvivesLocalRecheck(t *testing.T) {
	store := NewStore(t.TempDir())
	d := boundaryRecording(t, store)
	for i := range d.Parts {
		d.Parts[i].Transcribed = true
	}
	d.Segments = []Segment{{ID: "boundary", Part: "mic-0001.wav", Track: "mic", Start: 1, End: 1.04, Text: "граница"}}
	if e := store.Save(d); e != nil {
		t.Fatal(e)
	}
	p := &fakeProcessor{}
	m := NewManager(store, nil, p)
	if e := m.transcribe(context.Background(), &d); e != nil {
		t.Fatal(e)
	}
	if len(d.Segments) != 1 || d.Segments[0].ID != "boundary" || p.calls != 0 {
		t.Fatalf("saved boundary speech removed: %+v", d.Segments)
	}
}
