package diagnostics

import (
	"archive/zip"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// ArchiveSessions is how many recent sessions an archive includes.
const ArchiveSessions = 5

// maxArchivedFile bounds each copied file: a session log is limited to
// MaxBytes plus the error reserve, a crash file only by the Go runtime.
const maxArchivedFile = DefaultMaxBytes + 2*errorReserve

// Archive writes <dest>/diagnostics-<UTC stamp>.zip with the newest session
// logs and crash files of this session's folder plus summary.txt, and returns
// its path. The archive appears under its final name only when complete.
func (s *Session) Archive(dest, summary string) (string, error) {
	s.Flush(2 * time.Second)
	return buildArchive(s.dir, dest, s.now().UTC(), summary)
}

func buildArchive(dir, dest string, now time.Time, summary string) (string, error) {
	names, files, err := sessionFiles(dir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dest, 0700); err != nil {
		return "", err
	}
	out, err := os.OpenRoot(dest)
	if err != nil {
		return "", err
	}
	defer out.Close()
	in, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp := ".diagnostics-" + rand.Text()
	f, err := out.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	done := false
	defer func() {
		if !done {
			_ = out.Remove(tmp)
		}
	}()
	z := zip.NewWriter(f)
	err = addFile(z, "summary.txt", now, func(w io.Writer) error {
		_, err := io.WriteString(w, summary)
		return err
	})
	for _, name := range names[:min(ArchiveSessions, len(names))] {
		for _, file := range files[name] {
			if err != nil {
				break
			}
			err = addFile(z, file, now, func(w io.Writer) error { return copyRegular(in, file, w) })
		}
	}
	err = errors.Join(err, z.Close())
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return "", err
	}
	base := "diagnostics-" + now.Format(stampLayout)
	for i := 0; i < 100; i++ {
		name := base + ".zip"
		if i > 0 {
			name = fmt.Sprintf("%s-%02d.zip", base, i)
		}
		if _, err := out.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := out.Rename(tmp, name); err != nil {
			return "", err
		}
		done = true
		return filepath.Join(dest, name), nil
	}
	return "", errors.New("не удалось подобрать имя архива диагностики")
}

func addFile(z *zip.Writer, name string, at time.Time, fill func(io.Writer) error) error {
	w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: at})
	if err != nil {
		return err
	}
	return fill(w)
}

func copyRegular(root *os.Root, name string, w io.Writer) error {
	f, err := root.Open(name)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("файл журнала заменён другим объектом")
	}
	_, err = io.Copy(w, io.LimitReader(f, maxArchivedFile))
	return err
}
