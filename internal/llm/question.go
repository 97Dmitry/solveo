package llm

import (
	"context"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/questionbuilder"
)

// QuestionAnalyzer is an isolated auxiliary operation, never a conversational Generate.
type QuestionAnalyzer interface {
	AnalyzeQuestion(context.Context, questionbuilder.Input, chat.Config) (questionbuilder.Result, error)
}
