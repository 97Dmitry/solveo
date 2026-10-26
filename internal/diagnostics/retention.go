package diagnostics

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"regexp"
	"slices"
)

var fileNamePattern = regexp.MustCompile(`^(?:session-([0-9]{8}-[0-9]{6}-[0-9]{1,10})\.log|crash-([0-9]{8}-[0-9]{6}-[0-9]{1,10})\.txt)$`)

// sessionFiles groups the regular log and crash files of dir by session name,
// newest first. Other files, directories and links are never listed.
func sessionFiles(dir string) ([]string, map[string][]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	files := map[string][]string{}
	for _, e := range entries {
		m := fileNamePattern.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		name := m[1] + m[2]
		files[name] = append(files[name], e.Name())
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int { return compareNames(b, a) })
	return names, files, nil
}

// compareNames orders by the UTC stamp, then numerically by PID.
func compareNames(a, b string) int {
	if c := compare(a[:15], b[:15]); c != 0 {
		return c
	}
	if c := compare(len(a), len(b)); c != 0 {
		return c
	}
	return compare(a, b)
}

func compare[T int | string](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// prune keeps the newest keep sessions and returns the newest remaining one.
func prune(dir string, keep int) (string, error) {
	names, files, err := sessionFiles(dir)
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	for _, name := range names[min(keep, len(names)):] {
		for _, f := range files[name] {
			// A file replaced by a link after listing is left alone.
			if info, err := root.Lstat(f); err == nil && info.Mode().IsRegular() {
				_ = root.Remove(f)
			}
		}
	}
	// Close cannot remove a crash file on Windows while the runtime still holds it;
	// empty ones of finished sessions are removed here.
	for _, name := range names[:min(keep, len(names))] {
		if info, err := root.Lstat(sessionCrash(name)); err == nil && info.Mode().IsRegular() && info.Size() == 0 {
			_ = root.Remove(sessionCrash(name))
		}
	}
	if len(names) == 0 || keep <= 0 {
		return "", nil
	}
	return names[0], nil
}

// inspectPrevious reports a non-empty crash file and a missing end record.
func inspectPrevious(root *os.Root, name string) (string, bool) {
	crash := ""
	if info, err := root.Lstat(sessionCrash(name)); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
		crash = sessionCrash(name)
	}
	f, err := root.Open(sessionLog(name))
	if err != nil {
		return crash, !errors.Is(err, fs.ErrNotExist)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return crash, true
	}
	const tail = 4096
	buf := make([]byte, min(info.Size(), tail))
	if _, err := f.ReadAt(buf, info.Size()-int64(len(buf))); err != nil && !errors.Is(err, io.EOF) {
		return crash, true
	}
	return crash, !bytes.Contains(buf, []byte(`msg="`+endMessage+`"`))
}
