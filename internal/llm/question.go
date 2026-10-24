package llm

import (
	"context"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/questionbuilder"
)

// QuestionAnalyzer is an isolated auxiliary operation, never a conversational Generate.
// emit receives only content-free progress events (timing lines for the native log).
type QuestionAnalyzer interface {
	AnalyzeQuestion(context.Context, questionbuilder.Input, chat.Config, func(Event)) (questionbuilder.Result, error)
}

// QuestionPreparer optionally keeps one empty analysis session for the next AnalyzeQuestion
// with the same model/effort. It blocks until that analysis claims it or ctx ends, sends no
// transcript and starts no model turn; cleanup finishes before it returns.
type QuestionPreparer interface {
	PrepareQuestionAnalysis(context.Context, chat.Config) error
}
