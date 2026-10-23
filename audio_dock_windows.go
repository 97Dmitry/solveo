//go:build windows && !bindings

package main

import (
	"errors"
	"sync"

	"github.com/97Dmitry/solveo/internal/audiowindow"
	native "github.com/97Dmitry/solveo/internal/platform/windows"
)

type audioDock struct {
	mu           sync.Mutex
	hwnd, parent uintptr
	parentPID    uint32
	state        audiowindow.DockState
	message      string
}

func (d *audioDock) attach() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	info, err := native.ReadAudioDockInfo(d.hwnd, d.parent, d.parentPID)
	if err != nil {
		return err
	}
	if !info.ParentVisible {
		return errors.New("Сначала покажите основное окно")
	}
	if info.AudioMinimized {
		return errors.New("Сначала восстановите окно аудио")
	}
	target, err := audiowindow.LeftDock(info.Parent, info.Work, info.Audio.Dx())
	if err != nil {
		return err
	}
	if err := native.MoveAudioDock(d.hwnd, target); err != nil {
		return err
	}
	d.state = audiowindow.DockState{Attached: true}
	d.message = ""
	return nil
}
func (d *audioDock) detach() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state = audiowindow.DockState{}
	d.message = ""
	return nil
}
func (d *audioDock) layout() (bool, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state.Attached, d.message
}
func (d *audioDock) manualHide() { d.mu.Lock(); d.state.ManualHide(); d.mu.Unlock() }
func (d *audioDock) manualShow() { d.mu.Lock(); d.state.ManualShow(); d.mu.Unlock() }
func (d *audioDock) follow() audiowindow.DockVisibility {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.state.Attached || d.hwnd == 0 {
		return audiowindow.DockKeep
	}
	info, err := native.ReadAudioDockInfo(d.hwnd, d.parent, d.parentPID)
	if err != nil {
		d.state.Attached = false
		d.message = err.Error()
		return audiowindow.DockKeep
	}
	action := d.state.Visibility(info.ParentVisible, info.AudioVisible)
	if !info.ParentVisible {
		return action
	}
	// Restore before reading the real width: a minimized HWND has icon geometry.
	if info.AudioMinimized {
		if d.state.UserHidden {
			return audiowindow.DockKeep
		}
		return audiowindow.DockShow
	}
	target, err := audiowindow.LeftDock(info.Parent, info.Work, info.Audio.Dx())
	if err == nil && target != info.Audio {
		err = native.MoveAudioDock(d.hwnd, target)
	}
	if err != nil {
		d.state.Attached = false
		d.message = "Окно аудио откреплено: " + err.Error()
	}
	return action
}
