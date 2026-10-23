package history

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/97Dmitry/solveo/internal/chat"
)

const exportsDir = "exports"

var exportNamePattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[^/\\:*?"<>|]{1,80}$`)

type ExportResult struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Missing int    `json:"missing"`
}

// Export writes a chat as exports/<UTC time>-<title>/chat.md with its images
// in images/NN.png. The folder appears only when complete: it is written under
// a temporary name and renamed. Images that cannot be read are counted in
// Missing and replaced by a note, so an old damaged chat still exports.
func (s *Store) Export(id string, now time.Time) (ExportResult, error) {
	c, e := s.Load(id)
	if e != nil {
		return ExportResult{}, e
	}
	root, e := os.OpenRoot(s.Dir)
	if e != nil {
		return ExportResult{}, e
	}
	defer root.Close()
	if e = root.MkdirAll(exportsDir, 0700); e != nil {
		return ExportResult{}, e
	}
	tmp := exportsDir + "/.tmp-" + rand.Text()
	if e = root.Mkdir(tmp, 0700); e != nil {
		return ExportResult{}, e
	}
	done := false
	defer func() {
		if !done {
			_ = root.RemoveAll(tmp)
		}
	}()
	files := map[string]string{}
	missing := 0
	if len(c.Attachments) > 0 {
		if e = root.Mkdir(tmp+"/images", 0700); e != nil {
			return ExportResult{}, e
		}
	}
	for i, a := range c.Attachments {
		name := fmt.Sprintf("images/%02d.png", i+1)
		if e := s.copyImage(root, c.ID, a, tmp+"/"+name); e != nil {
			missing++
			continue
		}
		files[a.ID] = name
	}
	if e = writeFile(root, tmp+"/chat.md", []byte(exportMarkdown(c, files, now))); e != nil {
		return ExportResult{}, e
	}
	base := now.UTC().Format("20060102-150405") + "-" + exportTitle(c.Title)
	for n := 1; n <= 100; n++ {
		name := base
		if n > 1 {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		if _, e := root.Lstat(exportsDir + "/" + name); !errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e = root.Rename(tmp, exportsDir+"/"+name); e != nil {
			return ExportResult{}, e
		}
		done = true
		return ExportResult{Name: name, Path: filepath.Join(s.Dir, exportsDir, name), Missing: missing}, nil
	}
	return ExportResult{}, errors.New("не удалось подобрать имя папки экспорта")
}

// ExportPath resolves a folder created by Export. Only plain names are accepted.
func (s *Store) ExportPath(name string) (string, error) {
	if !exportNamePattern.MatchString(name) || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return "", errors.New("некорректная папка экспорта")
	}
	root, e := os.OpenRoot(s.Dir)
	if e != nil {
		return "", e
	}
	defer root.Close()
	info, e := root.Lstat(exportsDir + "/" + name)
	if e != nil {
		return "", e
	}
	if !info.IsDir() {
		return "", errors.New("некорректная папка экспорта")
	}
	return filepath.Join(s.Dir, exportsDir, name), nil
}
func (s *Store) copyImage(root *os.Root, chatID string, a chat.Attachment, dst string) error {
	if !validPath(chatID, a.Path) {
		return errors.New("некорректный путь снимка в архиве")
	}
	in, e := root.Open(a.Path)
	if e != nil {
		return e
	}
	defer func() { _ = in.Close() }() // read-only
	out, e := root.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Close())
}
func writeFile(root *os.Root, name string, data []byte) error {
	f, e := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	return errors.Join(writeErr, f.Close())
}

// exportTitle keeps a readable, Windows-safe folder suffix.
func exportTitle(title string) string {
	var b strings.Builder
	n := 0
	for _, r := range strings.TrimSpace(title) {
		if n == 60 {
			break
		}
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		default:
			b.WriteRune('-')
		}
		n++
	}
	out := strings.TrimRight(strings.Join(strings.Fields(b.String()), " "), ". ")
	if out == "" {
		return "chat"
	}
	return out
}
func exportMarkdown(c chat.Chat, files map[string]string, now time.Time) string {
	const stamp = "2006-01-02 15:04 UTC"
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", c.Title)
	fmt.Fprintf(&b, "Создан: %s · обновлён: %s · экспортирован: %s\n",
		c.CreatedAt.UTC().Format(stamp), c.UpdatedAt.UTC().Format(stamp), now.UTC().Format(stamp))
	index := map[string]int{}
	for i, a := range c.Attachments {
		index[a.ID] = i + 1
	}
	used := map[string]bool{}
	image := func(id string) {
		used[id] = true
		if name, ok := files[id]; ok {
			fmt.Fprintf(&b, "\n![Снимок %d](%s)\n", index[id], name)
		} else {
			fmt.Fprintf(&b, "\n*Снимок %d недоступен.*\n", index[id])
		}
	}
	status := func(value, failure string) {
		if value != chat.StatusComplete {
			note := statusText(value)
			if failure != "" {
				note += ": " + failure
			}
			fmt.Fprintf(&b, "\n> %s\n", note)
		}
	}
	question := 0
	for _, m := range c.Messages {
		if m.Role == chat.RoleUser {
			question++
			fmt.Fprintf(&b, "\n## Вопрос %d\n", question)
			if strings.TrimSpace(m.Text) != "" {
				fmt.Fprintf(&b, "\n%s\n", strings.TrimRight(m.Text, "\n"))
			}
			for _, id := range m.AttachmentIDs {
				if index[id] > 0 {
					image(id)
				}
			}
			for _, a := range m.Answers {
				title := "Решение"
				if a.Kind == chat.AnswerApproach {
					title = "Подход (предварительный)"
				}
				fmt.Fprintf(&b, "\n### %s\n", title)
				if a.Text != "" {
					fmt.Fprintf(&b, "\n%s\n", strings.TrimRight(a.Text, "\n"))
				}
				status(a.Status, a.Error)
			}
			if len(m.Answers) == 0 {
				status(m.Status, lastAttemptError(m))
			}
			continue
		}
		fmt.Fprintf(&b, "\n### Ответ\n\n%s\n", strings.TrimRight(m.Text, "\n"))
		status(m.Status, "")
	}
	rest := false
	for _, a := range c.Attachments {
		if !used[a.ID] {
			if !rest {
				b.WriteString("\n## Снимки чата\n")
				rest = true
			}
			image(a.ID)
		}
	}
	return b.String()
}
func lastAttemptError(m chat.Message) string {
	if len(m.Attempts) == 0 {
		return ""
	}
	return m.Attempts[len(m.Attempts)-1].Error
}
func statusText(status string) string {
	switch status {
	case chat.StatusFailed:
		return "Ошибка"
	case chat.StatusCanceled:
		return "Отменено"
	case chat.StatusInterrupted:
		return "Прервано"
	case chat.StatusUnknown:
		return "Исход неизвестен"
	case chat.StatusRequesting:
		return "Ответ не получен"
	}
	return "Статус: " + status
}
