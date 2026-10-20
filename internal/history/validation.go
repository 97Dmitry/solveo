package history

import (
	"errors"
	"github.com/97Dmitry/solveo/internal/chat"
)

func validStatus(status string) bool {
	switch status {
	case "requesting", "complete", "failed", "canceled", "unknown", "interrupted":
		return true
	}
	return false
}
func validateDocument(c chat.Chat) error {
	if !idPattern.MatchString(c.ID) || c.ProviderID == "" || c.ConfigRevision == 0 {
		return errors.New("invalid chat metadata")
	}
	switch c.Config.Detail {
	case "brief", "normal", "detailed":
	default:
		return errors.New("invalid answer detail in archive")
	}
	attachments := map[string]bool{}
	for _, a := range c.Attachments {
		if !idPattern.MatchString(a.ID) || attachments[a.ID] || !validPath(c.ID, a.Path) {
			return errors.New("invalid archive attachment")
		}
		attachments[a.ID] = true
	}
	messages := map[string]bool{}
	attempts := map[string]bool{}
	for _, m := range c.Messages {
		if !idPattern.MatchString(m.ID) || messages[m.ID] || (m.Role != "user" && m.Role != "assistant") || !validStatus(m.Status) {
			return errors.New("invalid archive message")
		}
		messages[m.ID] = true
		for _, id := range m.AttachmentIDs {
			if !attachments[id] {
				return errors.New("message refers to a missing attachment")
			}
		}
		for _, a := range m.Attempts {
			if !idPattern.MatchString(a.ID) || attempts[a.ID] || !validStatus(a.Status) {
				return errors.New("invalid archive attempt")
			}
			attempts[a.ID] = true
		}
	}
	return nil
}
