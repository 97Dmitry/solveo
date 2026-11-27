// Package codex talks to Codex CLI through one persistent, isolated
// `codex app-server` connection in read-only mode.
package codex

import (
	"log/slog"
	"time"

	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/llm/codex/appserver"
)

const DefaultTimeout = 10 * time.Minute
const ConnectionTimeout = time.Minute

type Client struct {
	Binary  string
	Timeout time.Duration
	Prompt  string
	// app is shared by every copy of the Client: the connection, its mutex and
	// login state live behind this pointer and are never copied.
	app            *appServer
	analysis       *appServer
	answers        *appServer
	answerAnalysis *appServer
}

// New uses home as Solveo's own CODEX_HOME and work as an empty working directory.
func New(binary, prompt string, timeout time.Duration, home, work string) *Client {
	c := &Client{Binary: binary, Prompt: prompt, Timeout: timeout}
	c.app = &appServer{binary: c.binary(), home: home, work: work, start: appserver.Start, preparationGate: make(chan struct{}, 1)}
	c.analysis = &appServer{analysis: true, binary: c.binary(), home: home, work: work, start: appserver.Start, preparationGate: make(chan struct{}, 1)}
	c.answers = &appServer{binary: c.binary(), home: home, work: work, start: appserver.Start, preparationGate: make(chan struct{}, 1)}
	c.answerAnalysis = &appServer{analysis: true, binary: c.binary(), home: home, work: work, start: appserver.Start, preparationGate: make(chan struct{}, 1)}
	return c
}

// SetLogger sends app-server process records to the session log. Call it
// before the first request.
func (c *Client) SetLogger(l *slog.Logger) {
	c.app.log = l
	c.analysis.log = l
	c.answers.log = l
	c.answerAnalysis.log = l
}

// AutomaticAnswers has its own lazy answer transport. Interrupting or retiring
// it does not affect the main chat. Authentication and shutdown stay with c.
func (c *Client) AutomaticAnswers() llm.Provider {
	scope := *c
	scope.app = c.answers
	scope.analysis = c.answerAnalysis
	return &scope
}

func (c Client) binary() string {
	if c.Binary == "" {
		return "codex"
	}
	return c.Binary
}
