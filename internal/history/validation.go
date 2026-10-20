package history

import (
	"errors"

	"github.com/97Dmitry/solveo/internal/chat"
)

func validStatus(status string) bool {
	switch status {
	case chat.StatusRequesting, chat.StatusComplete, chat.StatusFailed, chat.StatusCanceled, chat.StatusUnknown, chat.StatusInterrupted:
		return true
	}
	return false
}
func validateDocument(c chat.Chat) error {
	if !idPattern.MatchString(c.ID) || c.ProviderID == "" || c.ConfigRevision == 0 {
		return errors.New("повреждены метаданные чата")
	}
	switch c.Config.Detail {
	case "brief", "normal", "detailed":
	default:
		return errors.New("в архиве некорректная подробность ответа")
	}
	attachments := map[string]bool{}
	for _, a := range c.Attachments {
		if !idPattern.MatchString(a.ID) || attachments[a.ID] || !validPath(c.ID, a.Path) {
			return errors.New("в архиве некорректный снимок")
		}
		attachments[a.ID] = true
	}
	messages := map[string]bool{}
	attempts := map[string]bool{}
	for _, m := range c.Messages {
		if !idPattern.MatchString(m.ID) || messages[m.ID] || (m.Role != chat.RoleUser && m.Role != chat.RoleAssistant) || !validStatus(m.Status) {
			return errors.New("в архиве некорректное сообщение")
		}
		messages[m.ID] = true
		for _, id := range m.AttachmentIDs {
			if !attachments[id] {
				return errors.New("сообщение ссылается на отсутствующий снимок")
			}
		}
		for _, a := range m.Attempts {
			if !idPattern.MatchString(a.ID) || attempts[a.ID] || !validStatus(a.Status) {
				return errors.New("в архиве некорректная попытка запроса")
			}
			attempts[a.ID] = true
		}
	}
	return nil
}
