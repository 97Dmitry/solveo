package history

import (
	"encoding/json"
	"testing"

	"github.com/97Dmitry/solveo/internal/chat"
)

func TestParallelArchivePreservesCompletedApproachAndInterruptsMain(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	doc := chat.New()
	payload := `[{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","role":"user","text":"question","status":"requesting","attempts":[{"id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","status":"requesting"}],"answers":[{"kind":"approach","status":"complete","text":"Use a map","attempt_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","elapsed_ms":123},{"kind":"solution","status":"requesting","attempt_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}],"request_config":{"parallel_answers":true,"model":"original"}}]`
	if err := json.Unmarshal([]byte(payload), &doc.Messages); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(SaveInput{Chat: doc}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(got.Messages[0])
	var wire struct {
		Answers []struct {
			Kind, Status, Text string
			Elapsed            int64 `json:"elapsed_ms"`
		}
		Config *struct{ Model string } `json:"request_config"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Answers) != 2 || wire.Answers[0].Text != "Use a map" || wire.Answers[0].Status != "complete" || wire.Answers[0].Elapsed != 123 || wire.Answers[1].Status != "interrupted" || wire.Config == nil || wire.Config.Model != "original" {
		t.Fatalf("lost parallel recovery data: %s", encoded)
	}
}

func TestParallelArchiveRejectsAnswerFromUnrelatedAttempt(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	doc := chat.New()
	cfg := doc.Config
	cfg.ParallelAnswers = true
	attempt := chat.ID()
	doc.Messages = []chat.Message{{ID: chat.ID(), Role: chat.RoleUser, Status: chat.StatusRequesting, RequestConfig: &cfg, Attempts: []chat.Attempt{{ID: attempt, Status: chat.StatusRequesting}}, Answers: []chat.Answer{
		{Kind: chat.AnswerApproach, Status: chat.StatusComplete, Text: "approach", AttemptID: attempt},
		{Kind: chat.AnswerSolution, Status: chat.StatusRequesting, AttemptID: chat.ID()},
	}}}
	if _, err := s.Save(SaveInput{Chat: doc}); err == nil {
		t.Fatal("accepted an answer belonging to an unknown attempt")
	}
}
