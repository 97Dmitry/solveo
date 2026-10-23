package codex

import (
	"cmp"
	"errors"
	"slices"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/questionbuilder"
)

// Sources are data attached to automatic cards, never extra user instructions.
// Legacy cards have no reliable origin: preserve their old submission behavior.
type questionSource struct {
	Seq   uint64 `json:"seq"`
	ID    string `json:"id"`
	Track string `json:"track"`
	Text  string `json:"text"`
}

func questionSources(card *questionbuilder.Card) []questionSource {
	if card == nil || card.Origin != questionbuilder.OriginAutomatic {
		return nil
	}
	sources := make([]questionSource, 0, len(card.Sources))
	for _, s := range card.Sources {
		sources = append(sources, questionSource{ID: s.ID, Track: s.Track, Text: s.Text, Seq: s.Seq})
	}
	slices.SortStableFunc(sources, func(a, b questionSource) int { return cmp.Compare(a.Seq, b.Seq) })
	return sources
}
func previousQuestion(r llm.Request) *chat.Message {
	if r.Turn.Question == nil || r.Turn.Question.PreviousQuestionID == "" {
		return nil
	}
	for i := range r.History {
		if r.History[i].ID == r.Turn.Question.PreviousQuestionID && r.History[i].Role == chat.RoleUser {
			return &r.History[i]
		}
	}
	return nil
}
func validateQuestionSources(r llm.Request) error {
	card := r.Turn.Question
	if card == nil || card.Origin != questionbuilder.OriginAutomatic {
		return nil
	}
	if err := questionbuilder.ValidateSources(card.Sources); err != nil {
		return err
	}
	if card.PreviousQuestionID != "" {
		previous := previousQuestion(r)
		if previous == nil {
			return errors.New("предыдущий вопрос недоступен для уточнения")
		}
		if previous.Question != nil && previous.Question.Origin == questionbuilder.OriginAutomatic {
			return questionbuilder.ValidateSources(previous.Question.Sources)
		}
	}
	return nil
}
