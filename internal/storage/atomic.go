// Package storage provides atomic, bounded document replacement inside a root.
package storage

import (
	"crypto/rand"
	"errors"
	"os"
)

func Replace(root *os.Root, name string, data []byte) error {
	// The random basename is generated without following any archive path.
	base := ".solveo-" + rand.Text()
	f, err := root.OpenFile(base, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(base)
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return root.Rename(base, name)
}
