package qwen

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/97Dmitry/solveo/internal/stt"
)

func TestProviderDescribesLocalQwen(t *testing.T) {
	d := (&Provider{}).Describe()
	if d.ID != "qwen" || !d.Local || !d.Preparable || d.StartLabel != "Запустить модель" || d.MaxPhrases != 256 || d.MaxPhraseRunes != 128 {
		t.Fatalf("description %+v", d)
	}
}

func TestStatusCarriesLabels(t *testing.T) {
	runner := newFake()
	healthy(runner)
	p := &Provider{Manager: manager(t, runner)}
	writeModel(t, p.Manager.ModelsDir)
	var labels []string
	for _, c := range p.Status(context.Background()) {
		labels = append(labels, c.Label)
		if c.State != stt.StateOK {
			t.Fatalf("check %+v", c)
		}
	}
	if strings.Join(labels, ",") != "Docker,Docker Engine,Образ solveo-stt,GPU,Модель Qwen3-ASR" {
		t.Fatalf("labels %v", labels)
	}
}

type nopSession struct{ stt.SessionAPI }

func TestStartDialsTheStartedWorker(t *testing.T) {
	runner := newFake()
	healthy(runner)
	p := &Provider{Manager: manager(t, runner)}
	writeModel(t, p.Manager.ModelsDir)
	var addr, token string
	want := nopSession{}
	p.dial = func(_ context.Context, a, tok string) (stt.SessionAPI, error) {
		addr, token = a, tok
		return want, nil
	}
	got, err := p.Start(context.Background(), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if got != want || !strings.HasPrefix(addr, "127.0.0.1:") || len(token) != 64 {
		t.Fatalf("dialled %q %q", addr, token)
	}
}

func TestDialFailureStopsTheContainer(t *testing.T) {
	runner := newFake()
	healthy(runner)
	p := &Provider{Manager: manager(t, runner)}
	writeModel(t, p.Manager.ModelsDir)
	started := false
	runner.on("run -d", func([]string) (string, error) { started = true; return "id", nil })
	runner.on("container inspect", func([]string) (string, error) {
		if !started {
			return "", errors.New("No such container")
		}
		return "stt", nil
	})
	p.dial = func(context.Context, string, string) (stt.SessionAPI, error) { return nil, errors.New("refused") }
	if _, err := p.Start(context.Background(), func(string) {}); err == nil {
		t.Fatal("expected the dial error")
	}
	if len(runner.called("rm -f solveo-stt")) != 1 {
		t.Fatalf("container left running: %v", runner.calls)
	}
}
