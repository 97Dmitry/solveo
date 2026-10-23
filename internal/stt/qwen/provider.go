package qwen

import (
	"context"

	"github.com/97Dmitry/solveo/internal/stt"
)

// Provider is the local Qwen3-ASR recognizer: Manager owns the container, the session speaks protocol v1.
type Provider struct {
	Manager *Manager
	dial    func(ctx context.Context, addr, token string) (stt.SessionAPI, error)
}

// New uses the docker CLI from PATH and keeps the model under modelsDir.
func New(modelsDir string, getenv func(string) string) *Provider {
	return &Provider{Manager: &Manager{Runner: ExecRunner{Binary: "docker"}, ModelsDir: modelsDir, Getenv: getenv, Health: stt.Health}}
}

func (p *Provider) Describe() stt.Description {
	return stt.Description{
		ID: "qwen", Label: "Qwen3-ASR (локально)", Local: true, Preparable: true,
		StartLabel: "Запустить модель", StopLabel: "Выгрузить модель", MaxPhrases: 256, MaxPhraseRunes: 128,
	}
}

func (p *Provider) Status(ctx context.Context) []stt.Check { return p.Manager.Status(ctx) }

func (p *Provider) Prepare(ctx context.Context, progress func(string)) error {
	return p.Manager.Prepare(ctx, progress)
}

// Start runs the container, waits for the warmed-up model and connects.
func (p *Provider) Start(ctx context.Context, progress func(string)) (stt.SessionAPI, error) {
	ep, err := p.Manager.Start(ctx, progress)
	if err != nil {
		return nil, err
	}
	dial := p.dial
	if dial == nil {
		dial = func(ctx context.Context, addr, token string) (stt.SessionAPI, error) {
			s, err := stt.Dial(ctx, addr, token)
			if err != nil {
				return nil, err // never a typed nil inside the interface
			}
			return s, nil
		}
	}
	session, err := dial(ctx, ep.Addr, ep.Token)
	if err != nil {
		p.Manager.removeOwned(ctx)
		return nil, err
	}
	return session, nil
}

func (p *Provider) Stop(ctx context.Context) error { return p.Manager.Stop(ctx) }
