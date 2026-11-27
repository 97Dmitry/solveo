package audiowindow

import (
	"context"
	"errors"
	"net/http"
	"testing"

	core "github.com/97Dmitry/solveo/internal/app"
)

const answerSession = "11111111111111111111111111111111"
const answerCard = "22222222222222222222222222222222"

type fakeAnswerReader struct{}

func (fakeAnswerReader) AutoAnswerDetail(sessionID, cardID string) (core.AutoAnswerDetail, error) {
	if sessionID != answerSession || cardID != answerCard {
		return core.AutoAnswerDetail{}, errors.New("unknown answer")
	}
	return core.AutoAnswerDetail{Question: "Полный вопрос"}, nil
}

func TestAnswersTransportSeparatesRendererStateAndCommands(t *testing.T) {
	audio, err := New(func() View { return View{ChatID: "audio chat"} }, func(context.Context, Command) error { return nil }, fakeAudioHistoryReader{})
	if err != nil {
		t.Fatal(err)
	}
	defer audio.Close()
	answers, err := NewAnswers(func() View {
		return View{ChatID: "private main chat", Theme: "light", UIScale: 110, Answers: &core.AutoAnswersView{SessionID: answerSession}}
	}, func(_ context.Context, cmd Command) error {
		if cmd.Name == "insertAnswer" && (cmd.ChatID != answerSession || len(cmd.IDs) != 1 || cmd.IDs[0] != answerCard) {
			return errors.New("wrong insert target")
		}
		return nil
	}, fakeAnswerReader{})
	if err != nil {
		t.Fatal(err)
	}
	defer answers.Close()
	audioClient, err := NewClient(audio.Config())
	if err != nil {
		t.Fatal(err)
	}
	answerClient, err := NewClient(answers.Config())
	if err != nil {
		t.Fatal(err)
	}
	if err := audioClient.Execute(Command{Name: "showAnswers"}); err != nil {
		t.Fatal(err)
	}
	insert := Command{Name: "insertAnswer", ChatID: answerSession, IDs: []string{answerCard}}
	if err := answerClient.Execute(insert); err != nil {
		t.Fatal(err)
	}
	if err := answerClient.Execute(Command{Name: "diagnostic", Diagnostic: &core.RendererError{Kind: "error", Message: "failed render"}}); err != nil {
		t.Fatal(err)
	}
	if err := audioClient.Execute(insert); err == nil {
		t.Fatal("audio token authorized an answers command")
	}
	for _, name := range []string{"showAnswers", "insert", "meeting", "question", "setKey", "pinAnswers"} {
		if err := answerClient.Execute(Command{Name: name}); err == nil {
			t.Fatalf("answers server authorized %s", name)
		}
	}
	if err := answerClient.Execute(Command{Name: "openAnswers", ChatID: "../chats"}); err == nil {
		t.Fatal("invalid session ID reached dispatch")
	}
	if err := answerClient.Execute(Command{Name: "retryAnswer", ChatID: answerSession, IDs: []string{answerCard, answerCard}}); err == nil {
		t.Fatal("multiple card IDs reached dispatch")
	}
	if err := answerClient.Execute(Command{Name: "discardAnswer", ChatID: answerSession, IDs: []string{answerCard}}); err == nil {
		t.Fatal("recovery without an exact attempt reached dispatch")
	}
	if _, err := answerClient.ChatHistory(answerSession); err == nil {
		t.Fatal("answers client accessed audio history")
	}
	var raw map[string]interface{}
	if err := answerClient.request("/state", nil, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["theme"] != "light" || raw["ui_scale"] != float64(110) || raw["answers"] == nil || raw["show_version"] == nil || len(raw) != 4 {
		t.Fatal("answers state included metadata from another renderer", raw)
	}
	before, err := answerClient.ShowVersion()
	if err != nil {
		t.Fatal(err)
	}
	audio.Show()
	if got, err := answerClient.ShowVersion(); err != nil || got != before {
		t.Fatal("revealing audio also revealed answers", got, err)
	}
	answers.Show()
	if got, err := answerClient.ShowVersion(); err != nil || got <= before {
		t.Fatal("answers reveal was lost", got, err)
	}
	for _, token := range []string{"", audio.Config().Token} {
		req, err := http.NewRequest(http.MethodGet, answers.Config().URL+"/answer/"+answerSession+"/"+answerCard, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatal("detail was exposed outside the answers client", resp.StatusCode)
		}
	}
	if doc, err := answerClient.Answer(answerSession, answerCard); err != nil || doc.Question != "Полный вопрос" {
		t.Fatal(doc, err)
	}
	if _, err := audioClient.Answer(answerSession, answerCard); err == nil {
		t.Fatal("audio client accessed answer detail")
	}
	if _, err := answerClient.Answer(answerSession, answerCard+"/extra"); err == nil {
		t.Fatal("invalid answer path was accepted")
	}
}
