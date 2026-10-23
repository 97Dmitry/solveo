package audiowindow

import (
	"errors"
	"image"
)

// LeftDock returns physical screen bounds without shrinking or moving the main window.
func LeftDock(parent, work image.Rectangle, width int) (image.Rectangle, error) {
	if width <= 0 || parent.Empty() || work.Empty() {
		return image.Rectangle{}, errors.New("Некорректная геометрия окна")
	}
	result := image.Rect(parent.Min.X-width-8, parent.Min.Y, parent.Min.X-8, parent.Max.Y)
	if !result.In(work) {
		return image.Rectangle{}, errors.New("Слева недостаточно места для окна аудио; переместите основное окно или уменьшите окно аудио")
	}
	return result, nil
}

type DockVisibility int

const (
	DockKeep DockVisibility = iota
	DockHide
	DockShow
)

type DockState struct {
	Attached       bool
	HiddenByParent bool
	UserHidden     bool
}

func (d *DockState) Visibility(parentVisible, childVisible bool) DockVisibility {
	if !d.Attached || d.UserHidden {
		return DockKeep
	}
	if !parentVisible && childVisible {
		d.HiddenByParent = true
		return DockHide
	}
	if parentVisible && d.HiddenByParent {
		d.HiddenByParent = false
		return DockShow
	}
	return DockKeep
}
func (d *DockState) ManualHide() { d.HiddenByParent = false; d.UserHidden = true }
func (d *DockState) ManualShow() { d.HiddenByParent = false; d.UserHidden = false }
