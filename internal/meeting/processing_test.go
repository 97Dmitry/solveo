package meeting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/audio"
)

type fakeProcessor struct {
	calls int
	fail  bool
}

func (p *fakeProcessor) Transcribe(context.Context, AudioInput) ([]Word, error) {
	p.calls++
	if p.fail {
		return nil, errors.New("network")
	}
	return []Word{{Text: "Решили", Start: 0, End: .04}, {Text: "проверить", Start: .04, End: .08}}, nil
}
func (*fakeProcessor) Summarize(_ context.Context, s []Segment) (Summary, error) {
	return Summary{Overview: "Проверка", Topics: []string{}, Decisions: []Item{{Text: "Проверить", Sources: []string{s[0].ID}}}, Tasks: []Item{}, Questions: []string{}}, nil
}
func (*fakeProcessor) Cleanup(context.Context) error { return nil }
func waitJob(t *testing.T, m *Manager) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for m.State().ProcessingID != "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if m.State().ProcessingID != "" {
		t.Fatal("job stuck")
	}
}
func TestProcessRetryAndSummary(t *testing.T) {
	p := &fakeProcessor{fail: true}
	capture := &syntheticCapture{}
	m := NewManager(NewStore(t.TempDir()), capture, p)
	if e := m.Start(context.Background(), "Meeting", "", []string{"mic"}); e != nil {
		t.Fatal(e)
	}
	id := m.State().RecordingID
	samples := make([]float32, 1600)
	for i := range samples {
		samples[i] = .01
	}
	capture.push(audio.Mic, audio.Frame{Rate: 16000, Channels: 1, Samples: samples})
	if e := m.Stop(); e != nil {
		t.Fatal(e)
	}
	if e := m.Process(context.Background(), id, "transcribe", false); e != nil {
		t.Fatal(e)
	}
	waitJob(t, m)
	if m.State().Error == "" {
		t.Fatal("error hidden")
	}
	p.fail = false
	if e := m.Process(context.Background(), id, "transcribe", false); e != nil {
		t.Fatal(e)
	}
	waitJob(t, m)
	d, e := m.Load(id)
	if e != nil || len(d.Segments) == 0 || !d.Parts[0].Transcribed {
		t.Fatalf("%+v %v %s", d, e, m.State().Error)
	}
	if e = m.Process(context.Background(), id, "summary", false); e != nil {
		t.Fatal(e)
	}
	waitJob(t, m)
	d, e = m.Load(id)
	if e != nil || d.Summary == nil || p.calls != 2 {
		t.Fatalf("summary %+v %v calls %d err %s", d.Summary, e, p.calls, m.State().Error)
	}
}
func TestSummaryRejectsUnknownEvidence(t *testing.T) {
	if e := ValidateSummary(Summary{Overview: "text", Tasks: []Item{{Text: "task", Sources: []string{"invented"}}}}, []Segment{{ID: "real"}}); e == nil {
		t.Fatal("invented evidence accepted")
	}
}

type boundedSummaryProcessor struct {
	fakeProcessor
	largest int
}

func (p *boundedSummaryProcessor) Summarize(_ context.Context, s []Segment) (Summary, error) {
	b, _ := json.Marshal(s)
	p.largest = max(p.largest, len(b))
	return Summary{Overview: "Итог", Topics: []string{}, Decisions: []Item{{Text: "Решение", Sources: []string{s[0].ID}}}, Tasks: []Item{}, Questions: []string{}}, nil
}
func TestLongSummaryKeepsReducerBounded(t *testing.T) {
	store := NewStore(t.TempDir())
	d, e := store.New("Long", "", []string{"mic"})
	if e != nil {
		t.Fatal(e)
	}
	d.Status = "recorded"
	d.Parts = []Part{{File: "mic-0000.wav", Track: "mic", Rate: 16000, Samples: 16000, Complete: true, Transcribed: true}}
	for i := range 10000 {
		d.Segments = append(d.Segments, Segment{ID: fmt.Sprintf("seg-%d", i), Part: "mic-0000.wav", Track: "mic", Start: 0, End: 1, Text: strings.Repeat("слово ", 20)})
	}
	if e = store.Save(d); e != nil {
		t.Fatal(e)
	}
	p := &boundedSummaryProcessor{}
	m := NewManager(store, nil, p)
	if e = m.summarize(context.Background(), &d, false); e != nil {
		t.Fatal(e)
	}
	if p.largest > summaryBudget+1000 {
		t.Fatalf("unbounded reducer: %d", p.largest)
	}
}
