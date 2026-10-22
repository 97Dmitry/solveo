package app

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/hotkeys"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/platform"
)

type fakeCapture struct{}

func (fakeCapture) SavePrimary(p string) error {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		return err
	}
	return os.WriteFile(p, b.Bytes(), 0600)
}
func (fakeCapture) SaveRegion(p string, _ image.Rectangle) error {
	return (fakeCapture{}).SavePrimary(p)
}

type fakeHost struct {
	mu         sync.Mutex
	selections int
	rebindErr  error
	folders    []string
	hotkeys    []hotkeys.Binding
	taken      map[uint32]bool
	closes     int
	closeErr   error
}

func (h *fakeHost) BeginSelection(string) error              { h.selections++; return nil }
func (*fakeHost) ShowLog() error                             { return nil }
func (*fakeHost) ShowImages([]platform.ViewImage, int) error { return nil }
func (h *fakeHost) CloseImages() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closes++
	return h.closeErr
}
func (*fakeHost) SetTheme(bool) error { return nil }
func (*fakeHost) SetLog(string) error { return nil }
func TestQuickFailureWithNineteenAttachments(t *testing.T) {
	c, f, _ := setup(t)
	c.deps.Capturer = fakeCapture{}
	snap := c.Snapshot()
	for i := 0; i < 19; i++ {
		id := chat.ID()
		p, err := c.deps.Work.Path(id)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("png"), 0600); err != nil {
			t.Fatal(err)
		}
		c.queues[snap.ActiveChatID] = append(c.queues[snap.ActiveChatID], queued{ID: id, Path: p})
	}
	if err := c.SetDraft(DraftInput{ChatID: snap.ActiveChatID, Text: "draft stays", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if e := c.BeginCapture("quick-full"); e != nil {
		t.Fatal(e)
	}
	var r llm.Request
	select {
	case r = <-f.started:
	case <-time.After(5 * time.Second):
		t.Fatalf("quick capture did not reach provider: %+v", c.Snapshot())
	}
	if len(r.Images) != 1 || r.Turn.Text != "" {
		t.Fatal("quick shot included unrelated input")
	}
	f.finish <- errors.New("offline")
	waitIdle(t, c)
	snap = c.Snapshot()
	if len(snap.Queue) != 20 || snap.Draft.Text != "draft stays" {
		t.Fatal(snap)
	}
}
func TestFullQueueRejectsBeforeSelection(t *testing.T) {
	c, _, _ := setup(t)
	host := &fakeHost{}
	c.deps.NativeHost = host
	for i := 0; i < 20; i++ {
		c.queues[c.active.ID] = append(c.queues[c.active.ID], queued{ID: chat.ID()})
	}
	if e := c.BeginCapture("quick-region"); !errors.Is(e, ErrQueueFull) {
		t.Fatal(e)
	}
	if host.selections != 0 {
		t.Fatal("selector started with full queue")
	}
}
func TestSubmitIntentExpires(t *testing.T) {
	c, f, _ := setup(t)
	now := time.Now()
	c.deps.Now = func() time.Time { return now }
	intent, e := c.RequestSubmit()
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(5 * time.Second)
	_, e = c.Submit(SubmitInput{RequestID: chat.ID(), IntentID: intent.ID, ChatID: intent.ChatID, Text: "stale"})
	if !errors.Is(e, ErrStale) || f.calls != 0 {
		t.Fatal(e)
	}
}
func TestDraftIsolation(t *testing.T) {
	c, _, _ := setup(t)
	old := c.Snapshot().ActiveChatID
	if err := c.SetDraft(DraftInput{ChatID: old, Text: "old", Revision: 2}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetDraft(DraftInput{ChatID: old, Text: "stale", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.NewChat(); err != nil {
		t.Fatal(err)
	}
	if c.Snapshot().Draft.Text != "" {
		t.Fatal("draft leaked")
	}
	if _, err := c.OpenChat(old); err != nil {
		t.Fatal(err)
	}
	if c.Snapshot().Draft.Text != "old" {
		t.Fatal("draft lost")
	}
}

func (h *fakeHost) RebindImages(map[string]string) error { return h.rebindErr }
func (h *fakeHost) SetHotkeys(b []hotkeys.Binding) (map[string]error, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hotkeys = append([]hotkeys.Binding(nil), b...)
	failed := map[string]error{}
	for _, v := range b {
		if h.taken[v.Modifiers<<16|v.Key] {
			failed[v.Action] = errors.New("hot key is already registered")
		}
	}
	return failed, nil
}
func (h *fakeHost) OpenFolder(p string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.folders = append(h.folders, p)
	return nil
}
