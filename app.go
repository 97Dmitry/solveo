//go:build windows || bindings

package main

import (
	core "github.com/97Dmitry/solveo/internal/app"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/hotkeys"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/settings"
)

// App is the generated Wails boundary. All behavior lives in the Go controller.
type App struct {
	controller        *core.Controller
	showAudio         func() error
	audioInsertResult func(string, string)
	ready             func() error
	exit              func() error
}

func (a *App) Snapshot() core.Snapshot      { return a.controller.Snapshot() }
func (a *App) RendererReady() core.Snapshot { return a.controller.RendererReady() }
func (a *App) Ready() error {
	if a.ready != nil {
		return a.ready()
	}
	return nil
}
func (a *App) NewChat() (core.Snapshot, error)           { return a.controller.NewChat() }
func (a *App) OpenChat(id string) (core.Snapshot, error) { return a.controller.OpenChat(id) }
func (a *App) DeleteChats(ids []string) (core.DeleteResult, error) {
	return a.controller.DeleteChats(ids)
}
func (a *App) SetChatPinned(id string, pinned bool) error {
	return a.controller.SetChatPinned(id, pinned)
}
func (a *App) SearchHistory(query string) ([]core.SearchHit, error) {
	return a.controller.SearchHistory(query)
}
func (a *App) ExportChat(id string) (history.ExportResult, error) { return a.controller.ExportChat(id) }
func (a *App) RevealExport(name string) error                     { return a.controller.RevealExport(name) }
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

func (a *App) SaveProfile(in core.ProfileInput) (core.Snapshot, error) {
	return a.controller.SaveProfile(in)
}
func (a *App) DeleteProfile(id string, revision uint64) (core.Snapshot, error) {
	return a.controller.DeleteProfile(id, revision)
}
func (a *App) ReloadProfiles() (core.Snapshot, error) { return a.controller.ReloadProfiles() }
func (a *App) NewChatFromProfile(id string, revision uint64) (core.Snapshot, error) {
	return a.controller.NewChatFromProfile(id, revision)
}

func (a *App) ImportAttachment(chatID, label, dataBase64 string) error {
	return a.controller.ImportAttachment(chatID, label, dataBase64)
}
func (a *App) ReuseAttachment(chatID, id string) error {
	return a.controller.ReuseAttachment(chatID, id)
}
func (a *App) ReorderAttachments(chatID string, ids []string) error {
	return a.controller.ReorderAttachments(chatID, ids)
}

func (a *App) SuspendHotkeys(suspend bool) error { return a.controller.SuspendHotkeys(suspend) }
func (a *App) DefaultHotkeys() map[string]string { return hotkeys.Defaults() }

func (a *App) AudioSnapshot() core.AudioView { return a.controller.AudioSnapshot() }
func (a *App) RefreshAudio() error           { return a.controller.RefreshAudio() }
func (a *App) PrepareAudio() error           { return a.controller.PrepareAudio() }
func (a *App) StartAudioModel() error        { return a.controller.StartAudioModel() }
func (a *App) StopAudioModel() error         { return a.controller.StopAudioModel() }
func (a *App) StartListening() error         { return a.controller.StartListening() }
func (a *App) StopListening() error          { return a.controller.StopListening() }
func (a *App) ClearTranscript() error        { return a.controller.ClearTranscript() }
func (a *App) SubmitTranscript(in core.SubmitTranscriptInput) (string, error) {
	return a.controller.SubmitTranscript(in)
}
func (a *App) SetSTTKey(provider, key string) error { return a.controller.SetSTTKey(provider, key) }
func (a *App) DeleteSTTKey(provider string) error   { return a.controller.DeleteSTTKey(provider) }
func (a *App) SetAudioProvider(id string) error     { return a.controller.SetAudioProvider(id) }
