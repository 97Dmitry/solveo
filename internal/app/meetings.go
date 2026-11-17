package app

import (
	"errors"
	"os"

	"github.com/97Dmitry/solveo/internal/meeting"
)

type MeetingCommand struct {
	Action  string   `json:"action"`
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Tracks  []string `json:"tracks"`
	Partial bool     `json:"partial"`
}

func (c *Controller) MeetingState() meeting.State {
	if c.deps.Meetings == nil {
		return meeting.State{}
	}
	return c.deps.Meetings.State()
}
func (c *Controller) MeetingList() ([]meeting.Info, error) {
	if c.deps.Meetings == nil {
		return []meeting.Info{}, nil
	}
	return c.deps.Meetings.List()
}
func (c *Controller) MeetingDetail(id string) (meeting.Document, error) {
	if c.deps.Meetings == nil {
		return meeting.Document{}, errors.New("Архив встреч недоступен")
	}
	return c.deps.Meetings.Load(id)
}
func (c *Controller) OpenMeetingAudio(id, file string) (*os.File, error) {
	if c.deps.Meetings == nil {
		return nil, errors.New("Архив встреч недоступен")
	}
	return c.deps.Meetings.OpenAudio(id, file)
}
func (c *Controller) MeetingCommand(cmd MeetingCommand) error {
	m := c.deps.Meetings
	if m == nil {
		return errors.New("Запись встреч недоступна")
	}
	c.mu.Lock()
	closed, ctx, chatID := c.closed, c.ctx, c.active.ID
	c.mu.Unlock()
	if closed {
		return errors.New("Приложение завершается")
	}
	switch cmd.Action {
	case "start":
		c.captureMu.Lock()
		defer c.captureMu.Unlock()
		a := c.AudioSnapshot()
		if a.Listening || a.Busy != "" {
			return errors.New("Сначала остановите живое распознавание")
		}
		return m.Start(ctx, cmd.Title, chatID, cmd.Tracks)
	case "stop":
		c.captureMu.Lock()
		defer c.captureMu.Unlock()
		return m.Stop()
	case "transcribe", "summary":
		return m.Process(ctx, cmd.ID, cmd.Action, cmd.Partial)
	case "cancel":
		m.Cancel()
		return nil
	case "recover":
		return m.Recover(cmd.ID)
	case "rename":
		return m.Rename(cmd.ID, cmd.Title)
	case "export":
		return m.Export(cmd.ID)
	case "folder":
		folder, e := m.Folder(cmd.ID)
		if e != nil {
			return e
		}
		if c.deps.NativeHost == nil {
			return errors.New("Открытие папки недоступно")
		}
		return c.deps.NativeHost.OpenFolder(folder)
	}
	return errors.New("Неизвестная команда встречи")
}
