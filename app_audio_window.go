//go:build windows || bindings

package main

import (
	"errors"
	"sync"

	core "github.com/97Dmitry/solveo/internal/app"
	"github.com/97Dmitry/solveo/internal/audiowindow"
)

// AudioWindowApp exposes only the parent-owned audio operations to the auxiliary renderer.
type AudioWindowApp struct {
	client   *audiowindow.Client
	dockLeft func() error
	float    func() error
	layout   func() (bool, string)
	ready    func() error
	show     func()
	lost     func()
	changed  func(audiowindow.View)
	mu       sync.Mutex
	version  uint64
	isReady  bool
}

func (a *AudioWindowApp) State() (audiowindow.View, error) {
	if a.client == nil {
		return audiowindow.View{}, errors.New("Окно аудио не подключено")
	}
	v, err := a.client.State()
	if err != nil {
		if a.lost != nil {
			a.lost()
		}
		return v, err
	}
	if a.changed != nil {
		a.changed(v)
	}
	if a.layout != nil {
		v.Docked, v.WindowError = a.layout()
	}
	a.reveal(v.ShowVersion)
	return v, nil
}

// reveal is also driven by Go polling: hidden WebViews may throttle JS timers.
func (a *AudioWindowApp) reveal(version uint64) {
	a.mu.Lock()
	show := a.isReady && version > a.version
	if show {
		a.version = version
	}
	a.mu.Unlock()
	if show && a.show != nil {
		a.show()
	}
}
func (a *AudioWindowApp) Ready() error {
	if a.ready != nil {
		if err := a.ready(); err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.isReady = true
	a.mu.Unlock()
	_, err := a.State()
	return err
}
func (a *AudioWindowApp) Execute(cmd audiowindow.Command) error {
	if cmd.Name == "dockLeft" && a.dockLeft != nil {
		return a.dockLeft()
	}
	if cmd.Name == "float" && a.float != nil {
		return a.float()
	}
	if a.client == nil {
		return errors.New("Окно аудио не подключено")
	}
	return a.client.Execute(cmd)
}
func (a *App) ShowAudioWindow() error {
	if a.showAudio == nil {
		return errors.New("Окно аудио недоступно")
	}
	return a.showAudio()
}
func (a *App) CompleteAudioInsert(id, message string) {
	if a.audioInsertResult != nil {
		a.audioInsertResult(id, message)
	}
}

func (a *AudioWindowApp) HistoryForChat(id string) (core.AudioHistoryView, error) {
	if a.client == nil {
		return core.AudioHistoryView{}, errors.New("Окно аудио не подключено")
	}
	return a.client.ChatHistory(id)
}
func (a *AudioWindowApp) DeleteChatHistory(id string) error {
	if a.client == nil {
		return errors.New("Окно аудио не подключено")
	}
	return a.client.Execute(audiowindow.Command{Name: "deleteChatHistory", ChatID: id})
}
