package chat

import "testing"

func TestReferencedAnswer(t *testing.T) {
	messages := []Message{
		{ID: "ordinary", Role: RoleAssistant, Status: StatusComplete, Text: "first"},
		{ID: "parallel", Role: RoleUser, Status: StatusFailed, Answers: []Answer{{Kind: AnswerApproach, Status: StatusComplete, Text: "idea"}, {Kind: AnswerSolution, Status: StatusFailed, Text: "partial"}}},
		{ID: "unfinished", Role: RoleAssistant, Status: StatusInterrupted, Text: "partial"},
	}
	for _, tt := range []struct {
		id, kind, want string
		ok             bool
	}{
		{"ordinary", "", "first", true}, {"parallel", AnswerApproach, "idea", true},
		{"parallel", AnswerSolution, "", false}, {"ordinary", AnswerSolution, "", false},
		{"missing", "", "", false}, {"unfinished", "", "", false}, {"parallel", "", "", false},
	} {
		got, ok := ReferencedAnswer(messages, tt.id, tt.kind)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("%+v: %q %v", tt, got, ok)
		}
	}
}
