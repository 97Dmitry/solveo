//go:build windows

package windows

import (
	"errors"
	"image"
	"unsafe"
)

// PlaceAudioWindow keeps a new detached window within the nearest monitor's work area.
func PlaceAudioWindow(hwnd uintptr, x, y, width, height int) error {
	monitor, _, _ := user32.NewProc("MonitorFromWindow").Call(hwnd, 2)
	info := struct {
		size          uint32
		monitor, work rect
		flags         uint32
	}{}
	info.size = uint32(unsafe.Sizeof(info))
	if ok, _, _ := user32.NewProc("GetMonitorInfoW").Call(monitor, uintptr(unsafe.Pointer(&info))); ok == 0 {
		return errors.New("monitor work area unavailable")
	}
	// Pick the monitor containing the proposed position, rather than the default window position.
	pt := uint64(uint32(int32(x))) | uint64(uint32(int32(y)))<<32
	if m, _, _ := user32.NewProc("MonitorFromPoint").Call(uintptr(pt), 2); m != 0 {
		if ok, _, _ := user32.NewProc("GetMonitorInfoW").Call(m, uintptr(unsafe.Pointer(&info))); ok == 0 {
			return errors.New("monitor work area unavailable")
		}
	}
	width = min(width, int(info.work.right-info.work.left))
	height = min(height, int(info.work.bottom-info.work.top))
	x = max(int(info.work.left), min(x, int(info.work.right)-width))
	y = max(int(info.work.top), min(y, int(info.work.bottom)-height))
	if ok, _, _ := user32.NewProc("SetWindowPos").Call(hwnd, 0, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 0x14); ok == 0 {
		return errors.New("position audio window failed")
	}
	return nil
}

type AudioDockInfo struct {
	Parent, Audio, Work         image.Rectangle
	ParentVisible, AudioVisible bool
	AudioMinimized              bool
}

// ReadAudioDockInfo only reads HWND metadata. The parent is never moved or owned by the child.
func ReadAudioDockInfo(audio, parent uintptr, parentPID uint32) (AudioDockInfo, error) {
	var info AudioDockInfo
	var pid uint32
	thread, _, _ := user32.NewProc("GetWindowThreadProcessId").Call(parent, uintptr(unsafe.Pointer(&pid)))
	if thread == 0 || pid != parentPID {
		return info, errors.New("Основное окно недоступно")
	}
	var mainRect, audioRect rect
	if ok, _, _ := user32.NewProc("GetWindowRect").Call(parent, uintptr(unsafe.Pointer(&mainRect))); ok == 0 {
		return info, errors.New("Не удалось получить положение основного окна")
	}
	if ok, _, _ := user32.NewProc("GetWindowRect").Call(audio, uintptr(unsafe.Pointer(&audioRect))); ok == 0 {
		return info, errors.New("Не удалось получить положение окна аудио")
	}
	info.Parent = image.Rect(int(mainRect.left), int(mainRect.top), int(mainRect.right), int(mainRect.bottom))
	info.Audio = image.Rect(int(audioRect.left), int(audioRect.top), int(audioRect.right), int(audioRect.bottom))
	visible, _, _ := user32.NewProc("IsWindowVisible").Call(parent)
	iconic, _, _ := user32.NewProc("IsIconic").Call(parent)
	info.ParentVisible = visible != 0 && iconic == 0
	visible, _, _ = user32.NewProc("IsWindowVisible").Call(audio)
	info.AudioVisible = visible != 0
	iconic, _, _ = user32.NewProc("IsIconic").Call(audio)
	info.AudioMinimized = iconic != 0
	if !info.ParentVisible || info.AudioMinimized {
		return info, nil
	}
	x := info.Parent.Min.X - 8 - info.Audio.Dx()/2
	y := info.Parent.Min.Y + info.Parent.Dy()/2
	pt := uint64(uint32(int32(x))) | uint64(uint32(int32(y)))<<32
	monitor, _, _ := user32.NewProc("MonitorFromPoint").Call(uintptr(pt), 2)
	work := struct {
		size          uint32
		monitor, area rect
		flags         uint32
	}{}
	work.size = uint32(unsafe.Sizeof(work))
	if ok, _, _ := user32.NewProc("GetMonitorInfoW").Call(monitor, uintptr(unsafe.Pointer(&work))); ok == 0 {
		return info, errors.New("Не удалось получить рабочую область монитора")
	}
	info.Work = image.Rect(int(work.area.left), int(work.area.top), int(work.area.right), int(work.area.bottom))
	return info, nil
}

// MoveAudioDock queues placement on the Wails window thread, without activation or Z-order changes.
func MoveAudioDock(hwnd uintptr, bounds image.Rectangle) error {
	if bounds.Empty() {
		return errors.New("Некорректный размер окна аудио")
	}
	const flags = 0x4000 | 0x0010 | 0x0004 // SWP_ASYNCWINDOWPOS | SWP_NOACTIVATE | SWP_NOZORDER
	if ok, _, _ := user32.NewProc("SetWindowPos").Call(hwnd, 0, uintptr(bounds.Min.X), uintptr(bounds.Min.Y), uintptr(bounds.Dx()), uintptr(bounds.Dy()), flags); ok == 0 {
		return errors.New("Не удалось переместить окно аудио")
	}
	return nil
}
