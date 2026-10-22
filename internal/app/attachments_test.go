package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/97Dmitry/solveo/internal/chat"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func importImage(t *testing.T, c *Controller) string {
	t.Helper()
	if err := c.ImportAttachment(c.Snapshot().ActiveChatID, "picture.jpg", base64.StdEncoding.EncodeToString(pngBytes(t))); err != nil {
		t.Fatal(err)
	}
	s := c.Snapshot()
	return s.Queue[len(s.Queue)-1].ID
}
func queueIDs(c *Controller) []string {
	ids := []string{}
	for _, q := range c.Snapshot().Queue {
		ids = append(ids, q.ID)
	}
	return ids
}
func TestImportReorderPersistsAndProviderOrder(t *testing.T) {
	c, f, _ := setup(t)
	id := c.Snapshot().ActiveChatID
	a, b, d := importImage(t, c), importImage(t, c), importImage(t, c)
	order := []string{d, a, b}
	if err := c.ReorderAttachments(id, order); err != nil {
		t.Fatal(err)
	}
	st, _, err := c.deps.Work.Load()
	if err != nil || st.Chats[id].Queue[0].ID != d {
		t.Fatal(st, err)
	}
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: id, Text: "order", AttachmentIDs: []string{a, b, d}}); err != nil {
		t.Fatal(err)
	}
	r := <-f.started
	got := []string{}
	for _, im := range r.Images {
		got = append(got, im.ID)
	}
	if !reflect.DeepEqual(got, order) {
		t.Fatal(got, order)
	}
	f.finish <- errors.New("offline")
	waitIdle(t, c)
	if !reflect.DeepEqual(queueIDs(c), order) {
		t.Fatal(queueIDs(c))
	}
	reopened, _ := reopen(t, c)
	if !reflect.DeepEqual(queueIDs(reopened), order) {
		t.Fatal(queueIDs(reopened))
	}
}
func TestReuseHistoryHasIndependentOwnership(t *testing.T) {
	c, f, store := setup(t)
	id := c.Snapshot().ActiveChatID
	original := importImage(t, c)
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: id, AttachmentIDs: []string{original}}); err != nil {
		t.Fatal(err)
	}
	<-f.started
	f.finish <- nil
	waitIdle(t, c)
	originalPath, err := store.ImagePath(id, original)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ReuseAttachment(id, original); err != nil {
		t.Fatal(err)
	}
	copyID := queueIDs(c)[0]
	if err := c.ReuseAttachment(id, original); err != nil {
		t.Fatal(err)
	}
	if copyID == original || queueIDs(c)[1] == copyID {
		t.Fatal(queueIDs(c))
	}
	if err := c.RemoveAttachment(id, copyID); err != nil {
		t.Fatal(err)
	}
	remaining := queueIDs(c)[0]
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: id, AttachmentIDs: []string{remaining}}); err != nil {
		t.Fatal(err)
	}
	<-f.started
	f.finish <- nil
	waitIdle(t, c)
	after, err := os.ReadFile(originalPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("original changed", err)
	}
	reopened, _ := reopen(t, c)
	if len(reopened.Snapshot().Images) != 2 {
		t.Fatal(reopened.Snapshot().Images)
	}
}
func TestAttachmentGuardsAndInvalidInputKeepQueue(t *testing.T) {
	c, _, _ := setup(t)
	id := c.Snapshot().ActiveChatID
	a := importImage(t, c)
	for _, order := range [][]string{{}, {a, a}, {"unknown"}} {
		if err := c.ReorderAttachments(id, order); err == nil {
			t.Fatal("invalid order", order)
		}
	}
	if err := c.ImportAttachment("stale", "x", "bad"); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	if err := c.ImportAttachment(id, "x", "bad"); err == nil {
		t.Fatal("invalid base64")
	}
	if err := c.ImportAttachment(id, "x", base64.StdEncoding.EncodeToString([]byte("bad"))); err == nil {
		t.Fatal("invalid image")
	}
	if err := c.ReuseAttachment(id, a); err == nil {
		t.Fatal("unsent image is not history")
	}
	if !reflect.DeepEqual(queueIDs(c), []string{a}) {
		t.Fatal(queueIDs(c))
	}
	for i := 1; i < 20; i++ {
		importImage(t, c)
	}
	if err := c.ImportAttachment(id, "x", ""); !errors.Is(err, ErrQueueFull) {
		t.Fatal(err)
	}
	if err := c.ReuseAttachment(id, "anything"); !errors.Is(err, ErrQueueFull) {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.op = operation{Operation: Operation{Phase: phaseRequesting}}
	c.mu.Unlock()
	if err := c.ReorderAttachments(id, queueIDs(c)); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.op = operation{Operation: Operation{Phase: phaseIdle}}
	c.mu.Unlock()
}
func TestImportStateWriteFailureRetainsFileAndWorkError(t *testing.T) {
	c, _, _ := setup(t)
	work := &gatedWork{WorkStore: c.deps.Work}
	c.deps.Work = work
	work.set(errors.New("disk unavailable"), nil)
	defer work.set(nil, nil)
	imported := importImage(t, c)
	if c.Snapshot().WorkError == "" {
		t.Fatal("missing work error")
	}
	p, err := c.ResolveImage(c.Snapshot().ActiveChatID, imported)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	if err := c.ImportAttachment(c.Snapshot().ActiveChatID, "x", "bad"); err == nil {
		t.Fatal("invalid input accepted")
	}
	if c.Snapshot().WorkError == "" {
		t.Fatal("error lost")
	}
	work.set(nil, nil)
	persisted(t, c)
	if c.Snapshot().WorkError != "" {
		t.Fatal(c.Snapshot().WorkError)
	}
}
func TestImportLabelDoesNotContainPathsOrControls(t *testing.T) {
	c, _, _ := setup(t)
	if err := c.ImportAttachment(c.Snapshot().ActiveChatID, "C:\\secret\\folder\\photo.png\n\x00", base64.StdEncoding.EncodeToString(pngBytes(t))); err != nil {
		t.Fatal(err)
	}
	label := c.Snapshot().Queue[0].Label
	if strings.ContainsAny(label, "\\/:\n\x00") || label == "" {
		t.Fatal(label)
	}
}
func TestClosedControllerRejectsImport(t *testing.T) {
	c, _, _ := setup(t)
	id := c.Snapshot().ActiveChatID
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.ImportAttachment(id, "x", base64.StdEncoding.EncodeToString(pngBytes(t))); err == nil {
		t.Fatal("import after close")
	}
}

type blockedImageWork struct {
	WorkStore
	entered chan struct{}
	release chan struct{}
}

func (w *blockedImageWork) WriteImage(id string, data []byte) (string, error) {
	close(w.entered)
	<-w.release
	return w.WorkStore.WriteImage(id, data)
}
func TestCloseDrainsImportWithoutEnqueuingLateResult(t *testing.T) {
	c, _, _ := setup(t)
	id := c.Snapshot().ActiveChatID
	w := &blockedImageWork{WorkStore: c.deps.Work, entered: make(chan struct{}), release: make(chan struct{})}
	c.deps.Work = w
	imported := make(chan error, 1)
	encoded := base64.StdEncoding.EncodeToString(pngBytes(t))
	go func() { imported <- c.ImportAttachment(id, "image", encoded) }()
	<-w.entered
	if err := c.ImportAttachment(id, "another", encoded); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- c.Close(context.Background()) }()
	for {
		c.mu.Lock()
		closing := c.closed
		c.mu.Unlock()
		if closing {
			break
		}
		runtime.Gosched()
	}
	close(w.release)
	if err := <-imported; err == nil {
		t.Fatal("late import acknowledged")
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if len(queueIDs(c)) != 0 {
		t.Fatal("late image enqueued")
	}
}

type corruptCapture struct{ fakeCapture }

func (corruptCapture) SavePrimary(p string) error { return os.WriteFile(p, []byte("corrupt"), 0600) }
func TestCaptureRejectsCorruptPNGWithoutChangingQueue(t *testing.T) {
	c, _, _ := setup(t)
	before := importImage(t, c)
	c.deps.Capturer = corruptCapture{}
	if err := c.BeginCapture("quick-full"); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, c)
	if !reflect.DeepEqual(queueIDs(c), []string{before}) || c.Snapshot().Error == "" {
		t.Fatal(c.Snapshot())
	}
}
func TestQuickCapturePreservesReorderedQueueDraftAndReference(t *testing.T) {
	c, f, _ := setup(t)
	id := c.Snapshot().ActiveChatID
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: id, Text: "first"}); err != nil {
		t.Fatal(err)
	}
	<-f.started
	f.finish <- nil
	waitIdle(t, c)
	s := c.Snapshot()
	answer := s.Messages[len(s.Messages)-1].ID
	if err := c.SetDraft(DraftInput{ChatID: id, Text: "next", Revision: 7, ReplyToID: answer}); err != nil {
		t.Fatal(err)
	}
	a, b, d := importImage(t, c), importImage(t, c), importImage(t, c)
	order := []string{d, a, b}
	if err := c.ReorderAttachments(id, order); err != nil {
		t.Fatal(err)
	}
	c.deps.Capturer = fakeCapture{}
	if err := c.BeginCapture("quick-full"); err != nil {
		t.Fatal(err)
	}
	r := <-f.started
	if len(r.Images) != 1 || r.Turn.Text != "" || r.Turn.ReplyToID != "" {
		t.Fatal(r.Turn, r.Images)
	}
	f.finish <- nil
	waitIdle(t, c)
	s = c.Snapshot()
	if !reflect.DeepEqual(queueIDs(c), order) || s.Draft.Text != "next" || s.Draft.ReplyToID != answer {
		t.Fatal(s.Queue, s.Draft)
	}
}

func TestImportUnicodeFilenameFitsWorkStateLabelLimit(t *testing.T) {
	c, _, _ := setup(t)
	if err := c.ImportAttachment(c.Snapshot().ActiveChatID, strings.Repeat("я", 150)+".png", base64.StdEncoding.EncodeToString(pngBytes(t))); err != nil {
		t.Fatal(err)
	}
	s := c.Snapshot()
	if s.WorkError != "" {
		t.Fatal(s.WorkError)
	}
	if len(s.Queue[0].Label) > 200 {
		t.Fatal("label exceeds work state byte limit")
	}
}
