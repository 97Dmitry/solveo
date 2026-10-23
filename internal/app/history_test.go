package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/97Dmitry/solveo/internal/history"
)

// archiveHooks lets tests fail or block individual archive calls.
type archiveHooks struct {
	*history.Store
	failDelete string
	searching  chan struct{}
}

func (a *archiveHooks) Delete(id string) error {
	if id == a.failDelete {
		return errors.New("файл занят")
	}
	return a.Store.Delete(id)
}
func (a *archiveHooks) Search(ctx context.Context, q string, limit int) ([]history.SearchHit, error) {
	if a.searching != nil {
		a.searching <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return a.Store.Search(ctx, q, limit)
}

func chatIDs(s Snapshot) []string {
	out := []string{}
	for _, h := range s.History {
		out = append(out, h.ID)
	}
	return out
}
func TestDeleteActiveChatSwitchesToNewestRemaining(t *testing.T) {
	c, _, s := setup(t)
	c.deps.Capturer, c.deps.NativeHost = fakeCapture{}, &fakeHost{}
	first := c.Snapshot().ActiveChatID
	second := mustNewChat(t, c)
	third := mustNewChat(t, c)
	img := captureQueued(t, c)
	if e := c.SetDraft(DraftInput{ChatID: third, Text: "draft", Revision: 1}); e != nil {
		t.Fatal(e)
	}
	r, e := c.DeleteChats([]string{third, first})
	if e != nil || len(r.Deleted) != 2 || len(r.Failed) != 0 {
		t.Fatal(r, e)
	}
	snap := c.Snapshot()
	if snap.ActiveChatID != second || snap.Draft.Text != "" || len(snap.History) != 1 {
		t.Fatalf("%s %+v", snap.ActiveChatID, snap.History)
	}
	if hasQueuedFile(t, c.deps.Work, img) {
		t.Fatal("queue of deleted active chat kept")
	}
	if ok, _ := s.Exists(third); ok {
		t.Fatal("active chat not deleted")
	}
}
func TestDeleteOnlyChatOpensNewChat(t *testing.T) {
	c, _, _ := setup(t)
	only := c.Snapshot().ActiveChatID
	if r, e := c.DeleteChats([]string{only}); e != nil || len(r.Deleted) != 1 {
		t.Fatal(r, e)
	}
	snap := c.Snapshot()
	if snap.ActiveChatID == only || len(snap.History) != 1 || snap.History[0].ID != snap.ActiveChatID {
		t.Fatalf("%+v", snap.History)
	}
}
func TestPartialDeleteReportsEachChatAndKeepsFailedWork(t *testing.T) {
	c, _, s := setup(t)
	hooks := &archiveHooks{Store: s}
	c.deps.Store = hooks
	busy := c.Snapshot().ActiveChatID
	if e := c.SetDraft(DraftInput{ChatID: busy, Text: "keep me", Revision: 1}); e != nil {
		t.Fatal(e)
	}
	other := mustNewChat(t, c)
	active := mustNewChat(t, c)
	hooks.failDelete = busy
	r, e := c.DeleteChats([]string{busy, other})
	if e != nil || len(r.Deleted) != 1 || r.Deleted[0] != other || len(r.Failed) != 1 || r.Failed[0].ID != busy || r.Failed[0].Error != "файл занят" {
		t.Fatal(r, e)
	}
	snap := c.Snapshot()
	if snap.ActiveChatID != active || len(snap.History) != 2 {
		t.Fatal(chatIDs(snap))
	}
	if _, e := c.OpenChat(busy); e != nil || c.Snapshot().Draft.Text != "keep me" {
		t.Fatal("draft of a chat that failed to delete is lost", e)
	}
}
func TestDeleteRejectsDuplicateAndUnknownChats(t *testing.T) {
	c, _, _ := setup(t)
	id := c.Snapshot().ActiveChatID
	if _, e := c.DeleteChats([]string{id, id}); e == nil {
		t.Fatal("duplicate accepted")
	}
	if _, e := c.DeleteChats([]string{"missing"}); !errors.Is(e, ErrStale) {
		t.Fatal(e)
	}
	if _, e := c.DeleteChats(nil); e == nil {
		t.Fatal("empty selection accepted")
	}
}
func TestPinnedChatsComeFirstAndStayPinned(t *testing.T) {
	c, _, _ := setup(t)
	old := c.Snapshot().ActiveChatID
	newer := mustNewChat(t, c)
	if e := c.SetChatPinned(old, true); e != nil {
		t.Fatal(e)
	}
	if got := chatIDs(c.Snapshot()); got[0] != old || got[1] != newer || !c.Snapshot().History[0].Pinned {
		t.Fatal(got)
	}
	if _, e := c.OpenChat(old); e != nil {
		t.Fatal(e)
	}
	if !c.Snapshot().History[0].Pinned {
		t.Fatal("opening a chat lost its pin")
	}
	if e := c.SetChatPinned("missing", true); !errors.Is(e, ErrStale) {
		t.Fatal(e)
	}
}
func TestSearchHistoryFindsChatsAndCancelsOlderQuery(t *testing.T) {
	c, _, s := setup(t)
	snap := c.Snapshot()
	if _, e := c.SaveChatConfig(ConfigInput{ChatID: snap.ActiveChatID, Title: "Kafka интервью", Config: snap.Config, ExpectedRevision: snap.ConfigRevision}); e != nil {
		t.Fatal(e)
	}
	hits, e := c.SearchHistory("KAFKA")
	if e != nil || len(hits) != 1 || hits[0].ChatID != snap.ActiveChatID {
		t.Fatal(hits, e)
	}
	hooks := &archiveHooks{Store: s, searching: make(chan struct{})}
	c.deps.Store = hooks
	older := make(chan error, 1)
	go func() { _, e := c.SearchHistory("first"); older <- e }()
	<-hooks.searching
	go func() { _, _ = c.SearchHistory("second") }()
	<-hooks.searching
	if e := <-older; !errors.Is(e, errSearchReplaced) {
		t.Fatal(e)
	}
	// Close cancels and waits for the remaining scan.
}
func TestExportChatAndRevealFolder(t *testing.T) {
	c, f, _ := setup(t)
	host := &fakeHost{}
	c.deps.NativeHost = host
	snap := c.Snapshot()
	if _, e := c.Submit(SubmitInput{RequestID: "r1", ChatID: snap.ActiveChatID, Text: "вопрос"}); e != nil {
		t.Fatal(e)
	}
	<-f.started
	f.finish <- nil
	waitIdle(t, c)
	out, e := c.ExportChat(snap.ActiveChatID)
	if e != nil {
		t.Fatal(e)
	}
	md, e := os.ReadFile(filepath.Join(out.Path, "chat.md"))
	if e != nil || !containsAll(string(md), "вопрос", "### Ответ", "answer") {
		t.Fatal(string(md), e)
	}
	if e := c.RevealExport(out.Name); e != nil || len(host.folders) != 1 || host.folders[0] != out.Path {
		t.Fatal(host.folders, e)
	}
	if e := c.RevealExport("../chats"); e == nil {
		t.Fatal("revealed a path outside exports")
	}
}
func mustNewChat(t *testing.T, c *Controller) string {
	t.Helper()
	s, e := c.NewChat()
	if e != nil {
		t.Fatal(e)
	}
	return s.ActiveChatID
}
func containsAll(text string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(text, p) {
			return false
		}
	}
	return true
}
func TestDeletingActiveChatClosesImageViewerFirst(t *testing.T) {
	c, _, s := setup(t)
	host := &fakeHost{closeErr: errors.New("viewer busy")}
	c.deps.NativeHost = host
	other := c.Snapshot().ActiveChatID
	active := mustNewChat(t, c)
	if _, e := c.DeleteChats([]string{active}); e == nil {
		t.Fatal("deleted chat images while the viewer could still show them")
	}
	if ok, _ := s.Exists(active); !ok || c.Snapshot().ActiveChatID != active || c.Snapshot().Operation.Phase != "idle" {
		t.Fatal("failed viewer close must leave the chat untouched")
	}
	host.closeErr = nil
	if r, e := c.DeleteChats([]string{active}); e != nil || len(r.Deleted) != 1 || host.closes != 2 {
		t.Fatal(r, e, host.closes)
	}
	// Deleting inactive chats does not touch the viewer of the active chat.
	if _, e := c.DeleteChats([]string{mustNewChatThenReturn(t, c, other)}); e != nil || host.closes != 2 {
		t.Fatal(e, host.closes)
	}
}
func mustNewChatThenReturn(t *testing.T, c *Controller, back string) string {
	t.Helper()
	id := mustNewChat(t, c)
	if _, e := c.OpenChat(back); e != nil {
		t.Fatal(e)
	}
	return id
}
