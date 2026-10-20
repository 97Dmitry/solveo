//go:build windows

// Package instance prevents multiple GUI processes from sharing one archive.
package instance

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

var ErrAlreadyRunning = errors.New("Solveo уже запущен с этим архивом")

var createMutex = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")

// Acquire keeps a named kernel object alive for the lifetime of this process.
// Windows releases it even after a crash, so no stale lock files need cleanup.
// Separate archive directories may be used by independent instances.
func Acquire(dir string) (release func(), err error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(absolute))))
	name, err := syscall.UTF16PtrFromString(fmt.Sprintf(`Local\SolveoArchive-%x`, digest))
	if err != nil {
		return nil, err
	}
	handle, _, callErr := createMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return nil, fmt.Errorf("CreateMutexW: %w", callErr)
	}
	if callErr == syscall.ERROR_ALREADY_EXISTS {
		_ = syscall.CloseHandle(syscall.Handle(handle))
		return nil, ErrAlreadyRunning
	}
	var once sync.Once
	return func() { once.Do(func() { _ = syscall.CloseHandle(syscall.Handle(handle)) }) }, nil
}
