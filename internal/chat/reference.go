package chat

import "strings"

// ReferencedAnswer resolves a complete answer by its local identity. Empty kind
// selects an ordinary assistant message; parallel blocks require their kind.
func ReferencedAnswer(messages []Message, id, kind string) (string, bool) {
	if id == "" {
		return "", false
	}
	for _, m := range messages {
		if m.ID != id {
			continue
		}
		if kind == "" && m.Role == RoleAssistant && m.Status == StatusComplete && strings.TrimSpace(m.Text) != "" {
			return m.Text, true
		}
		if m.Role == RoleUser && (kind == AnswerApproach || kind == AnswerSolution) {
			for _, a := range m.Answers {
				if a.Kind == kind && a.Status == StatusComplete && strings.TrimSpace(a.Text) != "" {
					return a.Text, true
				}
			}
		}
		return "", false
	}
	return "", false
}

// ValidReference permits an absent reference, otherwise requires a complete answer.
func ValidReference(messages []Message, id, kind string) bool {
	if id == "" {
		return kind == ""
	}
	_, ok := ReferencedAnswer(messages, id, kind)
	return ok
}
