package meeting

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

func (m *Manager) Export(id string) error {
	if e := m.mutable(id); e != nil {
		return e
	}
	d, e := m.store.Load(id)
	if e != nil {
		return e
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s\n\n", d.Title, d.StartedAt.Format("2006-01-02 15:04 UTC"))
	if d.Summary != nil {
		s := d.Summary
		b.WriteString("## Summary\n\n")
		if s.Partial {
			b.WriteString("**Неполная запись**\n\n")
		}
		if s.Revision != d.Revision {
			b.WriteString("**Summary устарело**\n\n")
		}
		b.WriteString(s.Overview + "\n\n")
		for _, topic := range s.Topics {
			b.WriteString("- " + topic + "\n")
		}
		for _, group := range []struct {
			name  string
			items []Item
		}{{"Решения", s.Decisions}, {"Задачи", s.Tasks}} {
			b.WriteString("\n### " + group.name + "\n\n")
			for _, item := range group.items {
				fmt.Fprintf(&b, "- %s %s %s [%s]\n", item.Text, item.Owner, item.Deadline, strings.Join(item.Sources, ", "))
			}
		}
		b.WriteString("\n### Открытые вопросы\n\n")
		for _, q := range s.Questions {
			b.WriteString("- " + q + "\n")
		}
	}
	b.WriteString("\n## Транскрипт\n\n")
	for _, s := range d.Segments {
		fmt.Fprintf(&b, "[%s · %.1fs · %s · %s] %s\n\n", s.ID, s.Start, s.Track, s.Speaker, s.Text)
	}
	root, e := m.store.root(false)
	if e != nil {
		return errArchive
	}
	defer func() { _ = root.Close() }()
	f, e := root.OpenFile(id+"/meeting.md", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if e != nil {
		return errors.New("Не удалось экспортировать встречу")
	}
	_, e = f.WriteString(b.String())
	return errors.Join(e, f.Close())
}
func (m *Manager) Folder(id string) (string, error)            { return m.store.Folder(id) }
func (m *Manager) OpenAudio(id, file string) (*os.File, error) { return m.store.OpenAudio(id, file) }
