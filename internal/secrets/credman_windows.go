package secrets

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	advapi32        = windows.NewLazySystemDLL("advapi32.dll")
	procCredWriteW  = advapi32.NewProc("CredWriteW")
	procCredReadW   = advapi32.NewProc("CredReadW")
	procCredDeleteW = advapi32.NewProc("CredDeleteW")
	procCredFree    = advapi32.NewProc("CredFree")
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
)

// credential mirrors CREDENTIALW.
type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

type credentialManager struct{}

// NewCredentialManager stores generic credentials of the current Windows user.
func NewCredentialManager() Store { return credentialManager{} }

func (credentialManager) Get(name string) (string, bool, error) {
	target, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return "", false, err
	}
	var cred *credential
	r, _, callErr := procCredReadW.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&cred)))
	if r == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("не удалось прочитать %s из диспетчера учётных данных: %w", name, callErr)
	}
	defer func() { _, _, _ = procCredFree.Call(uintptr(unsafe.Pointer(cred))) }()
	blob := unsafe.Slice(cred.CredentialBlob, cred.CredentialBlobSize)
	return string(blob), true, nil
}

func (credentialManager) Set(name, value string) error {
	target, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	blob := []byte(value)
	if len(blob) == 0 {
		return errors.New("пустой секрет не сохраняется")
	}
	cred := credential{
		Type:               credTypeGeneric,
		TargetName:         target,
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     &blob[0],
		Persist:            credPersistLocalMachine,
	}
	if r, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0); r == 0 {
		return fmt.Errorf("не удалось сохранить %s в диспетчере учётных данных: %w", name, callErr)
	}
	return nil
}

func (credentialManager) Delete(name string) error {
	target, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	if r, _, callErr := procCredDeleteW.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0); r == 0 && !errors.Is(callErr, windows.ERROR_NOT_FOUND) {
		return fmt.Errorf("не удалось удалить %s из диспетчера учётных данных: %w", name, callErr)
	}
	return nil
}
