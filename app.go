//go:build windows || bindings

package main

import (
	core "github.com/97Dmitry/solveo/internal/app"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/settings"
)

// App is the generated Wails boundary. All behavior lives in the Go controller.
type App struct {
	controller *core.Controller
	ready      func() error
	exit       func() error
}

func (a *App) Snapshot() core.Snapshot      { return a.controller.Snapshot() }
func (a *App) RendererReady() core.Snapshot { return a.controller.RendererReady() }
func (a *App) Ready() error {
	if a.ready != nil {
		return a.ready()
	}
	return nil
}
func (a *App) NewChat() (core.Snapshot, error)             { return a.controller.NewChat() }
func (a *App) OpenChat(id string) (core.Snapshot, error)   { return a.controller.OpenChat(id) }
func (a *App) DeleteChat(id string) (core.Snapshot, error) { return a.controller.DeleteChat(id) }
func (a *App) SaveChatConfig(in core.ConfigInput) (core.Snapshot, error) {
	return a.controller.SaveChatConfig(in)
}
func (a *App) SetDraft(in core.DraftInput) error          { return a.controller.SetDraft(in) }
func (a *App) Submit(in core.SubmitInput) (string, error) { return a.controller.Submit(in) }
func (a *App) Cancel(id string) error                     { return a.controller.Cancel(id) }
func (a *App) BeginCapture(mode string) error             { return a.controller.BeginCapture(mode) }
func (a *App) RemoveAttachment(chatID, id string) error {
	return a.controller.RemoveAttachment(chatID, id)
}
func (a *App) ClearQueue(chatID string) error         { return a.controller.ClearQueue(chatID) }
func (a *App) RetrySave() error                       { return a.controller.RetrySave() }
func (a *App) DiscardUnsaved() (core.Snapshot, error) { return a.controller.DiscardUnsaved() }
func (a *App) UpdateSettings(in settings.Config) (core.Snapshot, error) {
	return a.controller.UpdateSettings(in)
}
func (a *App) SetAlwaysOnTop(value bool) (core.Snapshot, error) {
	return a.controller.SetAlwaysOnTop(value)
}
func (a *App) CheckConnection() (string, error)  { return a.controller.CheckConnection() }
func (a *App) OpenImage(chatID, id string) error { return a.controller.OpenImage(chatID, id) }
func (a *App) ShowLog() error                    { return a.controller.ShowLog() }

func (a *App) RequestSubmit() (core.SubmitIntent, error) { return a.controller.RequestSubmit() }

func (a *App) Exit() error {
	if a.exit != nil {
		return a.exit()
	}
	return nil
}

func (a *App) ListModels() ([]llm.Model, error) { return a.controller.ListModels() }

func (a *App) StreamSnapshot() core.StreamSnapshot { return a.controller.StreamSnapshot() }
func (a *App) Account() (llm.Account, error)       { return a.controller.Account() }
func (a *App) StartLogin() (core.Snapshot, error)  { return a.controller.StartLogin() }
func (a *App) CancelLogin() (core.Snapshot, error) { return a.controller.CancelLogin() }
