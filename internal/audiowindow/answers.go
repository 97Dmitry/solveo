package audiowindow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	core "github.com/97Dmitry/solveo/internal/app"
	"github.com/97Dmitry/solveo/internal/workstate"
)

type AnswerReader interface {
	AutoAnswerDetail(sessionID, cardID string) (core.AutoAnswerDetail, error)
}

// answersState limits frequent reads to preferences and answer metadata.
type answersState struct {
	Answers     *core.AutoAnswersView `json:"answers,omitempty"`
	Theme       string                `json:"theme"`
	UIScale     int                   `json:"ui_scale"`
	ShowVersion uint64                `json:"show_version"`
}

func NewAnswers(view func() View, execute func(context.Context, Command) error, reader AnswerReader) (*Server, error) {
	if reader == nil {
		return nil, errors.New("answer reader unavailable")
	}
	return newServer(view, execute, nil, reader)
}

func allowedAnswer(name string) bool {
	switch name {
	case "pauseAnswers", "startAnswers", "openAudioSettings", "newAnswers", "openAnswers", "retryAnswer", "insertAnswer", "discardAnswer", "diagnostic":
		return true
	}
	return false
}

func validAnswerTarget(cmd Command) bool {
	if cmd.ChatID != "" && !workstate.ValidID(cmd.ChatID) {
		return false
	}
	if cmd.Name == "retryAnswer" || cmd.Name == "discardAnswer" {
		return workstate.ValidID(cmd.ChatID) && len(cmd.IDs) == 1 && workstate.ValidID(cmd.IDs[0]) && workstate.ValidID(cmd.AnswerRevision)
	}
	return len(cmd.IDs) == 0 || (len(cmd.IDs) == 1 && workstate.ValidID(cmd.IDs[0]))
}

func serveAnswer(w http.ResponseWriter, r *http.Request, reader AnswerReader) {
	sessionID, cardID, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/answer/"), "/")
	if !workstate.ValidID(sessionID) || !workstate.ValidID(cardID) {
		http.NotFound(w, r)
		return
	}
	result, err := reader.AutoAnswerDetail(sessionID, cardID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}

func (c *Client) Answer(sessionID, cardID string) (core.AutoAnswerDetail, error) {
	var detail core.AutoAnswerDetail
	err := c.request("/answer/"+url.PathEscape(sessionID)+"/"+url.PathEscape(cardID), nil, &detail)
	return detail, err
}
