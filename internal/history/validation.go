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
	if !chat.ValidGlobalInstructions(c.GlobalInstructions) {
		return errors.New("в архиве некорректные общие инструкции")
	}
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
	messages := map[string]chat.Message{}
	attempts := map[string]bool{}
	for _, m := range c.Messages {
		target := messages[m.ReplyToID]
		if (m.ReplyToID != "" && m.Role != chat.RoleUser) || !chat.ValidReference([]chat.Message{target}, m.ReplyToID, m.ReplyToKind) {
			return errors.New("в архиве некорректная ссылка на ответ")
		}
		_, duplicate := messages[m.ID]
		if !idPattern.MatchString(m.ID) || duplicate || (m.Role != chat.RoleUser && m.Role != chat.RoleAssistant) || !validStatus(m.Status) {
			return errors.New("в архиве некорректное сообщение")
		}
		if len(m.Answers) > 0 {
			if m.Role != chat.RoleUser || len(m.Answers) != 2 || m.RequestConfig == nil || !m.RequestConfig.ParallelAnswers {
				return errors.New("в архиве некорректные блоки ответа")
			}
			for i, answer := range m.Answers {
				want := chat.AnswerApproach
				if i == 1 {
					want = chat.AnswerSolution
				}
				linked := false
				for _, attempt := range m.Attempts {
					if attempt.ID == answer.AttemptID {
						linked = true
						break
					}
				}
				if answer.Kind != want || !validStatus(answer.Status) || !idPattern.MatchString(answer.AttemptID) || !linked || answer.ElapsedMS < 0 {
					return errors.New("в архиве некорректный блок ответа")
				}
			}
		}
		messages[m.ID] = m
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
