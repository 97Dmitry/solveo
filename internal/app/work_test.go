package app

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/platform"
	"github.com/97Dmitry/solveo/internal/settings"
	"github.com/97Dmitry/solveo/internal/workstate"
)

// gatedWork injects write failures and lets a test hold a state write in progress.
type gatedWork struct {
	WorkStore
	mu      sync.Mutex
	fail    error
	gate    chan struct{}
	entered chan struct{}
}

func (w *gatedWork) Save(st workstate.State) error {
	w.mu.Lock()
	fail, gate := w.fail, w.gate
	w.mu.Unlock()
	if gate != nil {
		w.entered <- struct{}{}
		<-gate
	}
	if fail != nil {
		return fail
	}
	return w.WorkStore.Save(st)
}
func (w *gatedWork) set(fail error, gate chan struct{}) {
	w.mu.Lock()
	w.fail, w.gate = fail, gate
	w.mu.Unlock()
}

func newController(t *testing.T, store *history.Store, work WorkStore) (*Controller, *fakeProvider) {
	t.Helper()
	f := &fakeProvider{started: make(chan llm.Request, 5), finish: make(chan error, 5), store: store}
	c, e := New(Dependencies{Store: store, SettingsStore: settings.Open(store.Dir), Provider: f, Work: work, Capturer: fakeCapture{}, NativeHost: &fakeHost{}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _, _ = c.DiscardUnsaved(); _ = c.Close(context.Background()) })
	return c, f
}

// reopen starts a new controller on the same data, as after an application restart.
func reopen(t *testing.T, c *Controller) (*Controller, *fakeProvider) {
	t.Helper()
	return newController(t, c.deps.Store.(*history.Store), c.deps.Work)
}

// crash simulates the process dying after its last completed work write.
func crash(c *Controller) {
	c.statePersist.stop()
	c.streamPersist.stop()
}

// persisted performs the debounced work writes now.
func persisted(t *testing.T, c *Controller) {
	t.Helper()
	if e := errors.Join(c.statePersist.flush(), c.streamPersist.flush()); e != nil {
		t.Fatal(e)
	}
}

func captureQueued(t *testing.T, c *Controller) string {
	t.Helper()
	before := len(c.Snapshot().Queue)
	if e := c.BeginCapture("queue-region"); e != nil {
		t.Fatal(e)
	}
	op := c.Snapshot().Operation.ID
	c.HandleNativeEvent(platform.Event{Kind: "selection", OperationID: op, Region: image.Rect(1, 1, 10, 10)})
	waitIdle(t, c)
	q := c.Snapshot().Queue
	if len(q) != before+1 {
		t.Fatalf("capture not queued: %+v", c.Snapshot())
	}
	return q[len(q)-1].ID
}

func noProviderCall(t *testing.T, f *fakeProvider) {
	t.Helper()
	select {
	case r := <-f.started:
		t.Fatalf("restored work was submitted: %+v", r.Turn)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRestartKeepsNextQuestionDraft(t *testing.T) {
	c, f, _ := setup(t)
	id := c.Snapshot().ActiveChatID
	if e := c.SetDraft(DraftInput{ChatID: id, Text: "first", Revision: 1}); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: id, Text: "first", DraftRevision: 1}); e != nil {
		t.Fatal(e)
	}
	<-f.started
	if e := c.SetDraft(DraftInput{ChatID: id, Text: "second", Revision: 2}); e != nil {
		t.Fatal(e)
	}
	persisted(t, c)
	crash(c)
	r, rf := reopen(t, c)
	s := r.Snapshot()
	if s.ActiveChatID != id || s.Draft != (Draft{Text: "second", Revision: 2}) {
		t.Fatalf("%+v", s.Draft)
	}
	if len(s.Messages) != 1 || s.Messages[0].Status != chat.StatusInterrupted || !s.Controls.CanRetry {
		t.Fatalf("%+v", s.Messages)
	}
	noProviderCall(t, rf)
}

func TestRestartDoesNotDuplicateSubmittedDraft(t *testing.T) {
	c, f, _ := setup(t)
	id := c.Snapshot().ActiveChatID
	if e := c.SetDraft(DraftInput{ChatID: id, Text: "question", Revision: 3}); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: id, Text: "question", DraftRevision: 3}); e != nil {
		t.Fatal(e)
	}
	<-f.started
	persisted(t, c)
	crash(c)
	r, rf := reopen(t, c)
	s := r.Snapshot()
	if s.Draft.Text != "" || s.Draft.Revision != 4 || s.Messages[0].Text != "question" {
		t.Fatalf("%+v %+v", s.Draft, s.Messages)
	}
	noProviderCall(t, rf)
}

func TestDraftAndQueueSurviveRestart(t *testing.T) {
	c, _, _ := setup(t)
	c.deps.Capturer, c.deps.NativeHost = fakeCapture{}, &fakeHost{}
	first, second := captureQueued(t, c), captureQueued(t, c)
	id := c.Snapshot().ActiveChatID
	if e := c.SetDraft(DraftInput{ChatID: id, Text: "draft", Revision: 5}); e != nil {
		t.Fatal(e)
	}
	persisted(t, c)
	crash(c)
	r, _ := reopen(t, c)
	s := r.Snapshot()
	if len(s.Queue) != 2 || s.Queue[0].ID != first || s.Queue[1].ID != second || s.Draft.Text != "draft" {
		t.Fatalf("%+v %+v", s.Queue, s.Draft)
	}
	p, e := r.ResolveImage(id, first)
	if b, err := os.ReadFile(p); e != nil || err != nil || string(b) != "png" {
		t.Fatal(p, e, err)
	}
	if e = r.RemoveAttachment(id, first); e != nil {
		t.Fatal(e)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("removed capture kept")
	}
	crash(r)
	again, _ := reopen(t, r)
	if q := again.Snapshot().Queue; len(q) != 1 || q[0].ID != second {
		t.Fatalf("%+v", q)
	}
}

func TestSubmittedQueueIsClearedOnlyAfterArchive(t *testing.T) {
	c, f, _ := setup(t)
	c.deps.Capturer, c.deps.NativeHost = fakeCapture{}, &fakeHost{}
	img := captureQueued(t, c)
	id := c.Snapshot().ActiveChatID
	queuedPath, _ := c.deps.Work.Path(img)
	if _, e := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: id, AttachmentIDs: []string{img}}); e != nil {
		t.Fatal(e)
	}
	<-f.started
	if _, e := os.Stat(queuedPath); e != nil {
		t.Fatal("queued source removed before the answer was archived")
	}
	f.finish <- nil
	waitIdle(t, c)
	// Sources are removed by the submit worker right after it releases the operation.
	deadline := time.Now().Add(time.Second)
	for hasQueuedFile(t, c.deps.Work, img) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if _, e := os.Stat(queuedPath); !os.IsNotExist(e) {
		t.Fatal("source kept after archive")
	}
	crash(c)
	r, _ := reopen(t, c)
	s := r.Snapshot()
	if len(s.Queue) != 0 || len(s.Images) != 1 || s.Error != "" {
		t.Fatalf("%+v", s)
	}
}

func TestWorkWriteFailureIsVisibleAndRecovers(t *testing.T) {
	c0, _, store := setup(t)
	crash(c0)
	work := &gatedWork{WorkStore: c0.deps.Work}
	c, _ := newController(t, store, work)
	id := c.Snapshot().ActiveChatID
	work.set(errors.New("disk full"), nil)
	if e := c.SetDraft(DraftInput{ChatID: id, Text: "keep me", Revision: 1}); e != nil {
		t.Fatal(e)
	}
	if e := c.statePersist.flush(); e == nil {
		t.Fatal("write did not fail")
	}
	s := c.Snapshot()
	if !strings.Contains(s.WorkError, "disk full") || s.Draft.Text != "keep me" {
		t.Fatalf("%q %+v", s.WorkError, s.Draft)
	}
	work.set(nil, nil)
	persisted(t, c)
	if s = c.Snapshot(); s.WorkError != "" {
		t.Fatal(s.WorkError)
	}
	st, _, e := work.Load()
	if e != nil || st.Chats[id].Draft.Text != "keep me" {
		t.Fatal(st, e)
	}
}

func TestDelayedWriteDoesNotRestoreRemovedAttachment(t *testing.T) {
	c0, _, store := setup(t)
	crash(c0)
	work := &gatedWork{WorkStore: c0.deps.Work, entered: make(chan struct{}, 8)}
	c, _ := newController(t, store, work)
	a, b := captureQueued(t, c), captureQueued(t, c)
	id := c.Snapshot().ActiveChatID
	gate := make(chan struct{})
	work.set(nil, gate)
	// A draft write collects the queue with both images and is held on the way to disk.
	if e := c.SetDraft(DraftInput{ChatID: id, Text: "draft", Revision: 1}); e != nil {
		t.Fatal(e)
	}
	held := make(chan error, 1)
	go func() { held <- c.statePersist.flush() }()
	<-work.entered
	removed := make(chan error, 1)
	go func() { removed <- c.RemoveAttachment(id, a) }()
	deadline := time.Now().Add(time.Second)
	for len(c.Snapshot().Queue) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	work.set(nil, nil)
	close(gate)
	if e := errors.Join(<-held, <-removed); e != nil {
		t.Fatal(e)
	}
	crash(c)
	r, _ := reopen(t, c)
	if q := r.Snapshot().Queue; len(q) != 1 || q[0].ID != b {
		t.Fatalf("%+v", q)
	}
	if hasQueuedFile(t, work, a) {
		t.Fatal("removed image restored")
	}
}

func TestRestartReconcilesQueueFiles(t *testing.T) {
	c, _, _ := setup(t)
	c.deps.Capturer, c.deps.NativeHost = fakeCapture{}, &fakeHost{}
	lost := captureQueued(t, c)
	crash(c)
	p, _ := c.deps.Work.Path(lost)
	if e := os.Remove(p); e != nil {
		t.Fatal(e)
	}
	orphan, _ := c.deps.Work.Path("orphan")
	partial, _ := c.deps.Work.CapturePath("partial")
	for _, f := range []string{orphan, partial} {
		if e := os.WriteFile(f, []byte("png"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	r, _ := reopen(t, c)
	s := r.Snapshot()
	if len(s.Queue) != 0 || !strings.Contains(s.Error, "не найдено") {
		t.Fatalf("%+v %q", s.Queue, s.Error)
	}
	for _, f := range []string{orphan, partial} {
		if _, e := os.Stat(f); !os.IsNotExist(e) {
			t.Fatal("unreferenced file kept:", f)
		}
	}
}

func TestNewerWorkStateIsNeverOverwritten(t *testing.T) {
	c, _, store := setup(t)
	crash(c)
	file := filepath.Join(store.Dir, "work", "state.json")
	if e := os.WriteFile(file, []byte(`{"version":99}`), 0600); e != nil {
		t.Fatal(e)
	}
	r, _ := reopen(t, c)
	s := r.Snapshot()
	if !strings.Contains(s.WorkError, "более новой") {
		t.Fatal(s.WorkError)
	}
	if e := r.SetDraft(DraftInput{ChatID: s.ActiveChatID, Text: "memory only", Revision: 1}); e != nil {
		t.Fatal(e)
	}
	if e := r.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(file); string(b) != `{"version":99}` {
		t.Fatal(string(b))
	}
}

func TestDeleteChatRemovesItsWork(t *testing.T) {
	c, _, _ := setup(t)
	c.deps.Capturer, c.deps.NativeHost = fakeCapture{}, &fakeHost{}
	img := captureQueued(t, c)
	old := c.Snapshot().ActiveChatID
	if e := c.SetDraft(DraftInput{ChatID: old, Text: "draft", Revision: 1}); e != nil {
		t.Fatal(e)
	}
	if _, e := c.NewChat(); e != nil {
		t.Fatal(e)
	}
	if _, e := c.DeleteChat(old); e != nil {
		t.Fatal(e)
	}
	if hasQueuedFile(t, c.deps.Work, img) {
		t.Fatal("queued image of deleted chat kept")
	}
	st, _, e := c.deps.Work.Load()
	if _, ok := st.Chats[old]; e != nil || ok {
		t.Fatal(st, e)
	}
}

func TestWorkOfMissingChatMovesToNewChat(t *testing.T) {
	c, _, _ := setup(t)
	crash(c)
	if e := c.deps.Work.Save(workstate.State{Chats: map[string]workstate.Chat{"ghost": {Draft: workstate.Draft{Text: "unsaved", Revision: 2}}}}); e != nil {
		t.Fatal(e)
	}
	r, _ := reopen(t, c)
	s := r.Snapshot()
	if s.ActiveChatID == "ghost" || s.Draft.Text != "unsaved" || len(s.History) != 2 {
		t.Fatalf("%+v %+v", s.Draft, s.History)
	}
}

func TestDiscardMovesPersistedWork(t *testing.T) {
	c, f, s := setup(t)
	setGlobal(t, c, "recovery snapshot")
	c.deps.Capturer, c.deps.NativeHost = fakeCapture{}, &fakeHost{}
	img := captureQueued(t, c)
	old := c.Snapshot().ActiveChatID
	if _, e := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: old, Text: "q"}); e != nil {
		t.Fatal(e)
	}
	<-f.started
	backup := filepath.Join(s.Dir, "chats-backup")
	if e := os.Rename(filepath.Join(s.Dir, "chats"), backup); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(s.Dir, "chats"), []byte("blocked"), 0600); e != nil {
		t.Fatal(e)
	}
	f.finish <- nil
	waitIdle(t, c)
	snap, e := c.DiscardUnsaved()
	if e != nil {
		t.Fatal(e)
	}
	persisted(t, c)
	crash(c)
	_ = os.Remove(filepath.Join(s.Dir, "chats"))
	if e = os.Rename(backup, filepath.Join(s.Dir, "chats")); e != nil {
		t.Fatal(e)
	}
	// The replacement chat could not be archived: its queue moves to a new chat.
	r, _ := reopen(t, c)
	got := r.Snapshot()
	if got.ActiveChatID == old || got.ActiveChatID == snap.ActiveChatID || len(got.Queue) != 1 || got.Queue[0].ID != img {
		t.Fatalf("%+v", got)
	}
	doc, err := s.Load(got.ActiveChatID)
	if err != nil || doc.GlobalInstructions != "recovery snapshot" {
		t.Fatalf("missing recovery snapshot: %+v %v", doc, err)
	}

}

func TestStreamCheckpointRecoveredAfterCrash(t *testing.T) {
	c, p, _ := streamingSetup(t)
	submitText(t, c, "question")
	call := nextCall(t, p)
	call.emit(llm.Event{Kind: llm.EventAnswerText, Text: "partial answer"})
	persisted(t, c)
	crash(c)
	r, f := reopen(t, c)
	s := r.Snapshot()
	m := s.Messages[0]
	if m.Status != chat.StatusInterrupted || len(s.Recovered) != 1 || s.Recovered[0].Text != "partial answer" || s.Recovered[0].MessageID != m.ID {
		t.Fatalf("%+v %+v", m, s.Recovered)
	}
	noProviderCall(t, f)
	if _, e := r.Submit(SubmitInput{RequestID: chat.ID(), ChatID: s.ActiveChatID, RetryMessageID: m.ID}); e != nil {
		t.Fatal(e)
	}
	req := <-f.started
	for _, h := range append(req.History, req.Turn) {
		if strings.Contains(h.Text, "partial answer") {
			t.Fatal("recovered text reached the provider")
		}
	}
	if got := r.Snapshot().Recovered; len(got) != 0 {
		t.Fatal(got)
	}
	f.finish <- nil
	waitIdle(t, r)
	persisted(t, r)
	blocks, e := r.deps.Work.LoadStream()
	if e != nil || len(blocks) != 0 {
		t.Fatal(blocks, e)
	}
}

func TestRecoveredOnlyForInterruptedBlocks(t *testing.T) {
	doc := chat.Chat{Messages: []chat.Message{{ID: "m", Role: chat.RoleUser, Status: chat.StatusInterrupted, Attempts: []chat.Attempt{{ID: "a"}}, Answers: []chat.Answer{
		{Kind: chat.AnswerApproach, Status: chat.StatusComplete, AttemptID: "a"},
		{Kind: chat.AnswerSolution, Status: chat.StatusInterrupted, AttemptID: "a"},
	}}}}
	for block, want := range map[string]bool{blockApproach: false, blockSolution: true, blockMessage: false} {
		if got := interruptedBlock(doc, workstate.Block{MessageID: "m", AttemptID: "a", Block: block}); got != want {
			t.Fatal(block, got)
		}
	}
	if interruptedBlock(doc, workstate.Block{MessageID: "m", AttemptID: "old", Block: blockSolution}) {
		t.Fatal("matched an earlier attempt")
	}
}

func TestUnreadableQueuePreservesEntries(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix permission checks")
	}
	c, _, store := setup(t)
	c.deps.Capturer, c.deps.NativeHost = fakeCapture{}, &fakeHost{}
	img := captureQueued(t, c)
	persisted(t, c)
	crash(c)
	queueDir := filepath.Join(store.Dir, "work", "queue")
	path, _ := c.deps.Work.Path(img)
	if e := os.Chmod(queueDir, 0000); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.Chmod(queueDir, 0700) })
	r, _ := reopen(t, c)
	if e := os.Chmod(queueDir, 0700); e != nil {
		t.Fatal(e)
	}
	persisted(t, r)
	crash(r)
	again, _ := reopen(t, r)
	_, statErr := os.Stat(path)
	if len(again.Snapshot().Queue) != 1 || statErr != nil {
		t.Fatalf("unreadable queue was forgotten and source pruned after access recovered: queue=%v fileErr=%v", again.Snapshot().Queue, statErr)
	}
}

func TestCloseMustNotLoseDraftOnWriteFailure(t *testing.T) {
	old, _, store := setup(t)
	crash(old)
	work := &gatedWork{WorkStore: old.deps.Work}
	c, _ := newController(t, store, work)
	id := c.Snapshot().ActiveChatID
	work.set(errors.New("disk full"), nil)
	if e := c.SetDraft(DraftInput{ChatID: id, Text: "important unsaved question", Revision: 1}); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { work.set(nil, nil); _ = c.Close(context.Background()) })
	if err := c.Close(context.Background()); err == nil {
		t.Fatal("exit accepted despite failed final save")
	}
	if c.closed || c.statePersist.stopped || c.Snapshot().Draft.Text != "important unsaved question" {
		t.Fatal("failed exit discarded in-memory work or disabled retries")
	}
	work.set(nil, nil)
	// The refused exit must leave the controller usable, including a new draft.
	if err := c.SetDraft(DraftInput{ChatID: id, Text: "recovered disk", Revision: 2}); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, _, err := work.Load()
	if err != nil || st.Chats[id].Draft.Text != "recovered disk" {
		t.Fatalf("draft not saved on second exit: %+v %v", st, err)
	}
}

func TestSubmitWaitsForDurableMarker(t *testing.T) {
	old, _, store := setup(t)
	crash(old)
	work := &gatedWork{WorkStore: old.deps.Work}
	c, f := newController(t, store, work)
	id := c.Snapshot().ActiveChatID
	work.set(errors.New("disk full"), nil)
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: id, Text: "must stay local", DraftRevision: 1}); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, c)
	noProviderCall(t, f)
	if c.Snapshot().Draft.Text != "must stay local" || c.Snapshot().WorkError == "" || !c.Snapshot().Controls.CanDiscard {
		t.Fatal("failed submit must retain text, error and recovery")
	}
	work.set(nil, nil)
	if err := c.RetrySave(); err != nil {
		t.Fatal(err)
	}
}

func TestFastReplyDoesNotResurrectSubmittedDraft(t *testing.T) {
	c, f, _ := setup(t)
	id := c.Snapshot().ActiveChatID
	if e := c.SetDraft(DraftInput{ChatID: id, Text: "already submitted question", Revision: 1}); e != nil {
		t.Fatal(e)
	}
	persisted(t, c)
	if _, e := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: id, Text: "already submitted question", DraftRevision: 1}); e != nil {
		t.Fatal(e)
	}
	<-f.started
	f.finish <- nil
	waitIdle(t, c)
	crash(c)
	r, _ := reopen(t, c)
	if s := r.Snapshot(); s.Draft.Text != "" {
		t.Errorf("completed question resurrected as draft=%q; archived messages=%d", s.Draft.Text, len(s.Messages))
	}
}

func hasQueuedFile(t *testing.T, work WorkStore, id string) bool {
	t.Helper()
	present, err := work.Has(id)
	if err != nil {
		t.Fatal(err)
	}
	return present
}

func TestCloseDoesNotLoseAcceptedConcurrentDraft(t *testing.T) {
	old, _, store := setup(t)
	crash(old)
	work := &gatedWork{WorkStore: old.deps.Work, entered: make(chan struct{}, 2)}
	c, _ := newController(t, store, work)
	id := c.Snapshot().ActiveChatID
	if e := c.SetDraft(DraftInput{ChatID: id, Text: "before close", Revision: 1}); e != nil {
		t.Fatal(e)
	}
	gate := make(chan struct{})
	work.set(nil, gate)
	closed := make(chan error, 1)
	go func() { closed <- c.Close(context.Background()) }()
	<-work.entered
	editErr := c.SetDraft(DraftInput{ChatID: id, Text: "accepted during final write", Revision: 2})
	work.set(nil, nil)
	close(gate)
	if e := <-closed; e != nil {
		t.Fatal(e)
	}
	if editErr != nil {
		return
	} // Rejecting edits once closing is safe.
	st, _, e := work.Load()
	if e != nil || st.Chats[id].Draft.Text != "accepted during final write" {
		t.Fatalf("Close succeeded but lost accepted draft: saved=%q memory=%q error=%v", st.Chats[id].Draft.Text, c.Snapshot().Draft.Text, e)
	}
}

type startupWork struct {
	WorkStore
	entered chan struct{}
	release chan struct{}
	saved   chan struct{}
}

func (w *startupWork) LoadStream() ([]workstate.Block, error) {
	close(w.entered)
	<-w.release
	return w.WorkStore.LoadStream()
}
func (w *startupWork) Save(st workstate.State) error {
	select {
	case w.saved <- struct{}{}:
	default:
	}
	return w.WorkStore.Save(st)
}

func TestRestoreDoesNotStartWriterBeforeInitialization(t *testing.T) {
	old, f, store := setup(t)
	crash(old)
	if err := old.deps.Work.Save(workstate.State{Chats: map[string]workstate.Chat{
		"ghost": {Draft: workstate.Draft{Text: "restored draft", Revision: 1}},
	}}); err != nil {
		t.Fatal(err)
	}
	work := &startupWork{WorkStore: old.deps.Work, entered: make(chan struct{}), release: make(chan struct{}), saved: make(chan struct{}, 1)}
	type result struct {
		c   *Controller
		err error
	}
	done := make(chan result, 1)
	go func() {
		c, err := New(Dependencies{Store: store, SettingsStore: settings.Open(store.Dir), Provider: f, Work: work})
		done <- result{c, err}
	}()
	<-work.entered
	early := false
	select {
	case <-work.saved:
		early = true
	case <-time.After(2 * stateSaveDelay):
	}
	close(work.release)
	out := <-done
	if out.err != nil {
		t.Fatal(out.err)
	}
	t.Cleanup(func() { _ = out.c.Close(context.Background()) })
	if early {
		t.Fatal("background writer started before controller initialization completed")
	}
	// The deferred dirty state must still be written once initialization ends.
	persisted(t, out.c)
	st, _, err := work.Load()
	if err != nil || st.Chats[out.c.Snapshot().ActiveChatID].Draft.Text != "restored draft" {
		t.Fatalf("restored work was not saved after initialization: %+v %v", st, err)
	}
}
