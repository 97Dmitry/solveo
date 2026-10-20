package app

import (
	"context"
	"errors"
	"image"
	"testing"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/platform"
)

func TestPreparedTextImagesConfigRetryRestart(t *testing.T) {
	c, f, store := setup(t)
	c.deps.Capturer = fakeCapture{}
	c.deps.NativeHost = &fakeHost{}
	s := c.Snapshot()
	cfg := s.Config
	cfg.Context = "Go interview"
	if _, err := c.SaveChatConfig(ConfigInput{ChatID: s.ActiveChatID, Config: cfg, Title: "Interview", ExpectedRevision: s.ConfigRevision}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: s.ActiveChatID, Text: "Explain interfaces"}); err != nil {
		t.Fatal(err)
	}
	r := <-f.started
	if len(r.Images) != 0 || r.Config.Context != cfg.Context {
		t.Fatal(r)
	}
	f.finish <- nil
	waitIdle(t, c)
	for i := 0; i < 2; i++ {
		if e := c.BeginCapture("queue-region"); e != nil {
			t.Fatal(e)
		}
		op := c.Snapshot().Operation.ID
		c.HandleNativeEvent(platform.Event{Kind: "selection", OperationID: op, Region: image.Rect(1, 1, 10, 10)})
		waitIdle(t, c)
	}
	s = c.Snapshot()
	ids := []string{s.Queue[0].ID, s.Queue[1].ID}
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: s.ActiveChatID, Text: "Solve these", AttachmentIDs: ids}); err != nil {
		t.Fatal(err)
	}
	r = <-f.started
	if len(r.Images) != 2 || r.Images[0].ID != ids[0] || r.Images[1].ID != ids[1] {
		t.Fatal(r.Images)
	}
	f.finish <- nil
	waitIdle(t, c)
	s = c.Snapshot()
	cfg = s.Config
	cfg.Instructions = "Explain complexity"
	if _, err := c.SaveChatConfig(ConfigInput{ChatID: s.ActiveChatID, Title: s.Title, Config: cfg, ExpectedRevision: s.ConfigRevision}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: s.ActiveChatID, Text: "Next"}); err != nil {
		t.Fatal(err)
	}
	r = <-f.started
	if r.ConfigRevision != s.ConfigRevision+1 {
		t.Fatal(r.ConfigRevision)
	}
	f.finish <- errors.New("offline")
	waitIdle(t, c)
	s = c.Snapshot()
	last := s.Messages[len(s.Messages)-1]
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: s.ActiveChatID, RetryMessageID: last.ID}); err != nil {
		t.Fatal(err)
	}
	<-f.started
	f.finish <- nil
	waitIdle(t, c)
	reopened, e := New(Dependencies{Store: store, SettingsStore: c.deps.SettingsStore, Provider: f, TempDir: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = reopened.Close(context.Background()) }()
	got := reopened.Snapshot()
	if got.Title != "Interview" || len(got.Messages) != 6 || len(got.Images) != 2 || len(got.Messages[4].Attempts) != 2 {
		t.Fatal(got)
	}
}
