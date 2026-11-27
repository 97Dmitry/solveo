package app

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/questionbuilder"
)

type AutoAnswersView struct {
	SessionID string              `json:"session_id"`
	Running   bool                `json:"running"`
	State     string              `json:"state"`
	SetupHint string              `json:"setup_hint,omitempty"`
	Available bool                `json:"available"`
	Busy      bool                `json:"busy"`
	Error     string              `json:"error"`
	Items     []AutoAnswerItem    `json:"items"`
	Sessions  []AutoAnswerSession `json:"sessions"`
}

type AutoAnswerItem struct {
	ID              string `json:"id"`
	Question        string `json:"question"`
	Status          string `json:"status"`
	Revised         bool   `json:"revised"`
	Revision        string `json:"revision"`
	RevisionAttempt bool   `json:"revision_attempt"`
}

type AutoAnswerSession struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type AutoAnswerDetail struct {
	SessionID   string `json:"session_id"`
	ID          string `json:"id"`
	Question    string `json:"question"`
	Text        string `json:"text"`
	Error       string `json:"error"`
	Status      string `json:"status"`
	Revision    string `json:"revision"`
	SavedAnswer bool   `json:"saved_answer"`
}

// The parent alone owns connection/account lifecycle. The answer scope uses
// the same generation contract without publishing to the main renderer.
type autoProvider struct {
	llm.Provider
	analyzer llm.QuestionAnalyzer
}

func autoAnswerProvider(provider llm.Provider) autoProvider {
	if scope, ok := provider.(interface{ AutomaticAnswers() llm.Provider }); ok {
		provider = scope.AutomaticAnswers()
	}
	analyzer, _ := provider.(llm.QuestionAnalyzer)
	return autoProvider{Provider: provider, analyzer: analyzer}
}

func (p autoProvider) AnalyzeQuestion(ctx context.Context, in questionbuilder.Input, cfg chat.Config, emit func(llm.Event)) (questionbuilder.Result, error) {
	if p.analyzer == nil {
		return questionbuilder.Result{}, errors.New("Подключение не поддерживает сборку вопросов")
	}
	return p.analyzer.AnalyzeQuestion(ctx, in, cfg, emit)
}

func (c *Controller) AutoAnswersTheme() (string, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settings.Theme, c.settings.UIScale
}

// The latest question titles identify references to earlier cards. A few
// answer excerpts let the analyzer interpret short replies to clarifications.
func (c *Controller) autoAnalysisContextLocked() ([]questionbuilder.Card, map[string]string) {
	const maxQuestions = 30
	const maxAnswers = 6
	recent := make([]questionbuilder.Card, 0, maxQuestions)
	answers := make(map[string]string, maxAnswers)
	for i := len(c.active.Messages) - 1; i >= 0 && len(recent) < maxQuestions; i-- {
		m := c.active.Messages[i]
		if m.Role != chat.RoleUser || m.Question == nil {
			continue
		}
		card := autoRevisionBase(c.active, m)
		card.ID, card.Sources = m.ID, []questionbuilder.Segment{}
		card.Text = truncateRunes(card.Text, 320)
		recent = append(recent, card)
		if len(answers) < maxAnswers && i+1 < len(c.active.Messages) && c.active.Messages[i+1].Role == chat.RoleAssistant {
			runes := []rune(c.active.Messages[i+1].Text)
			const excerpt = 320
			if len(runes) > 2*excerpt {
				answers[m.ID] = string(runes[:excerpt]) + " … " + string(runes[len(runes)-excerpt:])
			} else {
				answers[m.ID] = string(runes)
			}
		}
	}
	slices.Reverse(recent)
	return recent, answers
}

func (c *Controller) AutoAnswersSnapshot() AutoAnswersView {
	view := AutoAnswersView{Items: []AutoAnswerItem{}, Sessions: []AutoAnswerSession{}, Error: c.autoError, State: "unavailable"}
	a := c.autoAnswers
	if a == nil {
		return view
	}
	status := c.autoStatus()
	a.mu.Lock()
	defer a.mu.Unlock()
	_, view.Available = c.deps.Provider.(llm.QuestionAnalyzer)
	view.SessionID = a.active.ID
	view.Running = a.question.automatic
	view.Busy = a.op.Phase != phaseIdle || status.starting
	view.State, view.Error = a.autoStatusLocked(status, view.Available)
	if view.State == "setup" {
		view.SetupHint = status.hint
	}
	for _, session := range a.summaries {
		view.Sessions = append(view.Sessions, AutoAnswerSession{ID: session.ID, Title: session.Title})
	}
	for i := range a.active.Messages {
		m := &a.active.Messages[i]
		if m.Role != chat.RoleUser {
			continue
		}
		title := []rune(strings.Join(strings.Fields(m.Text), " "))
		if len(title) > 240 {
			title = append(title[:239], '…')
		}
		revision := ""
		if last := len(m.Attempts) - 1; last >= 0 {
			revision = m.Attempts[last].ID
		}
		staged, revising := a.active.AnswerRevisions[m.ID]
		if revising {
			revision = staged.Attempt.ID
		}
		view.Items = append(view.Items, AutoAnswerItem{
			ID: m.ID, Question: string(title), Status: a.autoAnswerStatusLocked(i),
			Revised: m.Question != nil && m.Question.Refinement, Revision: revision, RevisionAttempt: revising,
		})
	}
	return view
}

func (c *Controller) AutoAnswersState() string {
	a := c.autoAnswers
	if a == nil {
		return "unavailable"
	}
	status := c.autoStatus()
	_, available := c.deps.Provider.(llm.QuestionAnalyzer)
	a.mu.Lock()
	defer a.mu.Unlock()
	state, _ := a.autoStatusLocked(status, available)
	return state
}

func (c *Controller) autoStatusLocked(status autoStatus, available bool) (state, err string) {
	b := c.questionLocked().View()
	err = c.err
	if err == "" {
		err = b.Error
	}
	state = status.state(available, c.question.automatic, c.op.Kind == kindSubmit && c.op.Phase != phaseSavePending,
		b.Status == "analyzing", b.Status == "draft", err)
	if status.err != "" {
		err = status.err
	}
	return state, err
}

func (c *Controller) latestAutoAnswerIndexLocked() int {
	last := len(c.active.Messages) - 1
	if last >= 0 && c.active.Messages[last].Role == chat.RoleAssistant {
		last--
	}
	return last
}

func (c *Controller) autoAnswerStatusLocked(index int) string {
	message := c.active.Messages[index]
	if c.op.Kind == kindSubmit && c.operationTargetsAnswerLocked(message.ID) {
		if c.pending != nil {
			return "pending"
		}
		if c.op.superseded || (message.Status == chat.StatusRequesting && message.Question != nil && message.Question.Refinement) {
			return "refining"
		}
	}
	if revision, ok := c.active.AnswerRevisions[message.ID]; ok {
		if revision.Attempt.Status == chat.StatusRequesting {
			return "refining"
		}
		return revision.Attempt.Status
	}
	return message.Status
}

func (c *Controller) operationTargetsAnswerLocked(cardID string) bool {
	if c.op.messageID != "" {
		return c.op.messageID == cardID
	}
	last := c.latestAutoAnswerIndexLocked()
	return last >= 0 && c.active.Messages[last].ID == cardID
}

func (c *Controller) AutoAnswerDetail(sessionID, cardID string) (AutoAnswerDetail, error) {
	a := c.autoAnswers
	if a == nil {
		return AutoAnswerDetail{}, errors.New("Автоответы недоступны")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if sessionID != a.active.ID {
		return AutoAnswerDetail{}, ErrStale
	}
	for i, m := range a.active.Messages {
		if m.ID != cardID || m.Role != chat.RoleUser {
			continue
		}
		out := AutoAnswerDetail{SessionID: sessionID, ID: m.ID, Question: m.Text, Status: a.autoAnswerStatusLocked(i)}
		if i+1 < len(a.active.Messages) && a.active.Messages[i+1].Role == chat.RoleAssistant {
			out.Text = a.active.Messages[i+1].Text
			out.SavedAnswer = true
		}
		if staged, ok := a.active.AnswerRevisions[m.ID]; ok {
			a.autoAnswerAttemptLocked(&out, staged.Attempt)
		} else if last := len(m.Attempts) - 1; last >= 0 {
			attempt := m.Attempts[last]
			a.autoAnswerAttemptLocked(&out, attempt)
		}
		if out.Status == chat.StatusInterrupted && out.Text == "" {
			for _, recovered := range a.recovered {
				if recovered.MessageID == m.ID && recovered.AttemptID == out.Revision {
					out.Text = recovered.Text
					break
				}
			}
		}
		return out, nil
	}
	return AutoAnswerDetail{}, ErrStale
}

func (c *Controller) autoAnswerAttemptLocked(out *AutoAnswerDetail, attempt chat.Attempt) {
	out.Revision = attempt.ID
	out.Error = attempt.Error
	if out.Status == "pending" {
		out.Error = c.err
		return
	}
	if c.op.superseded && out.Status == "refining" {
		out.Error = ""
		return
	}
	if preview, ok := c.streams[streamKey{c.op.ID, attempt.ID, blockMessage}]; ok {
		out.Text, out.SavedAnswer = preview.Text, false
	}
}

func (c *Controller) AutoAnswersCommand(action, sessionID, cardID, revisionID string) error {
	a := c.autoAnswers
	if a == nil {
		return errors.New("Автоответы недоступны")
	}
	c.mu.Lock()
	closed := c.closed
	starting := c.autoStart.cancel != nil
	active := chat.Chat{Config: c.active.Config, GlobalInstructions: c.active.GlobalInstructions}
	c.mu.Unlock()
	if closed {
		return ErrStale
	}
	if starting && (action == "newAnswers" || action == "openAnswers") {
		return ErrBusy
	}
	a.mu.Lock()
	match := a.active.ID == sessionID
	a.mu.Unlock()
	if action != "openAnswers" && !match {
		return ErrStale
	}
	switch action {
	case "startAnswers":
		return c.startAutomaticAnswers(sessionID)
	case "pauseAnswers":
		return c.pauseAutomaticAnswers(sessionID, "")
	case "newAnswers":
		return a.newAutoAnswersSession(sessionID, active)
	case "openAnswers":
		if sessionID == "" {
			return ErrStale
		}
		if match {
			return nil
		}
		_, err := a.OpenChat(sessionID)
		return err
	case "retryAnswer":
		if cardID == "" || revisionID == "" {
			return ErrStale
		}
		a.mu.Lock()
		pending := a.active.ID == sessionID && a.pending != nil
		a.mu.Unlock()
		if pending {
			return a.retrySave(sessionID, cardID, revisionID)
		}
		if handled, err := a.retryAutoRevision(sessionID, cardID, revisionID); handled {
			return err
		}
		_, err := a.submit(SubmitInput{ChatID: sessionID, RequestID: chat.ID(), RetryMessageID: cardID},
			submitOrigin{kind: originComposer, expectedAttempt: revisionID})
		return err
	case "discardAnswer":
		return a.discardAutoAnswer(sessionID, cardID, revisionID)
	default:
		return errors.New("Неизвестная команда автоответов")
	}
}

func (c *Controller) resumeAutoAnswers(id, model, reasoning string, preset chat.Chat) error {
	if _, ok := c.deps.Provider.(llm.QuestionAnalyzer); !ok {
		return errors.New("Подключение не поддерживает сборку вопросов")
	}
	if model == "" {
		return errors.New("Выберите модель сборщика в настройках аудио")
	}
	a := c.autoAnswers
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.active.ID != id {
		return ErrStale
	}
	if a.question.automatic {
		return nil
	}
	if err := a.availableLocked(); err != nil {
		return err
	}
	if a.active.ConfigRevision == 1 && len(a.active.Messages) == 0 {
		applyAutoAnswersPreset(&a.active, preset)
	}
	a.settings.Audio.BuilderModel = model
	a.settings.Audio.BuilderReasoning = reasoning
	a.autoSince = c.deps.Now().UTC()
	a.question.automatic = true
	a.err = ""
	b := a.questionLocked()
	b.Enable(true)
	for i := len(a.active.Messages) - 1; i >= 0; i-- {
		m := a.active.Messages[i]
		if m.Role != chat.RoleUser {
			continue
		}
		if m.Status == chat.StatusComplete && m.Question != nil {
			a.question.autoTurnID = m.ID
			if i+1 < len(a.active.Messages) && a.active.Messages[i+1].Role == chat.RoleAssistant {
				a.question.autoAnswerID = a.active.Messages[i+1].ID
			}
		}
		break
	}
	a.scheduleAutomaticQuestionLocked()
	return nil
}

func applyAutoAnswersPreset(doc *chat.Chat, preset chat.Chat) {
	doc.Config = preset.Config
	doc.Config.ParallelAnswers = false
	doc.ConfigRevision++
	doc.GlobalInstructions = preset.GlobalInstructions
}

func (c *Controller) newAutoAnswersSession(expectedID string, preset chat.Chat) error {
	c.mu.Lock()
	if err := c.availableLocked(); err != nil {
		c.mu.Unlock()
		return err
	}
	if c.active.ID != expectedID {
		c.mu.Unlock()
		return ErrStale
	}
	c.pauseQuestionLocked()
	c.op = operation{Operation: Operation{Kind: kindOpen, Phase: phaseSaving}}
	doc := newChat(preset.GlobalInstructions)
	applyAutoAnswersPreset(&doc, preset)
	c.mu.Unlock()
	saved, err := c.deps.Store.Save(history.SaveInput{Chat: doc})
	c.mu.Lock()
	defer c.mu.Unlock()
	c.op = operation{Operation: Operation{Phase: phaseIdle}}
	if err != nil {
		return err
	}
	c.active = saved
	c.rememberLocked(saved)
	c.err = ""
	c.changedLocked()
	return nil
}

func (c *Controller) pauseAutoAnswers(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if sessionID != "" && c.active.ID != sessionID {
		return
	}
	c.pauseQuestionLocked()
}

func (c *Controller) matchesPendingAutoAnswerLocked(sessionID, cardID, revisionID string) bool {
	if c.active.ID != sessionID || c.pending == nil || !c.operationTargetsAnswerLocked(cardID) || revisionID == "" {
		return false
	}
	if staged, ok := c.active.AnswerRevisions[cardID]; ok {
		return staged.Attempt.ID == revisionID
	}
	for _, m := range c.active.Messages {
		if m.ID == cardID && len(m.Attempts) > 0 {
			return m.Attempts[len(m.Attempts)-1].ID == revisionID
		}
	}
	return false
}

func (c *Controller) discardAutoAnswer(sessionID, cardID, revisionID string) error {
	c.mu.Lock()
	if !c.matchesPendingAutoAnswerLocked(sessionID, cardID, revisionID) || c.op.Phase != phaseSavePending {
		c.mu.Unlock()
		return ErrStale
	}
	c.op.Phase = phaseSaving
	revising := c.op.revising
	c.mu.Unlock()
	saved, err := c.deps.Store.Load(sessionID)
	if err == nil && revising {
		if _, exists := saved.AnswerRevisions[cardID]; exists {
			delete(saved.AnswerRevisions, cardID)
			saved, err = c.deps.Store.Save(history.SaveInput{Chat: saved})
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.op.Phase = phaseSavePending
		return err
	}
	c.active = saved
	delete(c.question.builders, sessionID)
	c.pending = nil
	c.op = operation{Operation: Operation{Phase: phaseIdle}}
	if c.clearCheckpointsLocked("") {
		c.streamPersist.mark()
	}
	c.err = ""
	c.changedLocked()
	return nil
}
