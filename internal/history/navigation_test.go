package history

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
)

func savedChatWithImage(t *testing.T, s *Store) chat.Chat {
	t.Helper()
	p := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(p, []byte("png"), 0600); err != nil {
		t.Fatal(err)
	}
	c := chat.New()
	c.Attachments = []chat.Attachment{{ID: chat.ID()}}
	saved, err := s.Save(SaveInput{Chat: c, Sources: []Source{{c.Attachments[0].ID, p}}})
	if err != nil {
		t.Fatal(err)
	}
	return saved
}
func exists(p string) bool { _, e := os.Lstat(p); return e == nil }

func TestPinsSurviveReopenAndDelete(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	a, b := savedChatWithImage(t, s), savedChatWithImage(t, s)
	if err := s.SetPinned(a.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPinned("missing", true); err == nil {
		t.Fatal("pinned a missing chat")
	}
	s, _ = Open(dir)
	list, _ := s.List()
	pinned := map[string]bool{}
	for _, v := range list {
		pinned[v.ID] = v.Pinned
	}
	if !pinned[a.ID] || pinned[b.ID] {
		t.Fatalf("pins: %+v", list)
	}
	if err := s.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	root, _ := os.OpenRoot(dir)
	defer root.Close()
	if pins, _ := s.readPins(root); pins[a.ID] {
		t.Fatal("deleted chat stays pinned")
	}
	if err := s.SetPinned(b.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPinned(b.ID, false); err != nil {
		t.Fatal(err)
	}
}
func TestMalformedPinsAreEmptyAndFuturePinsAreKept(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	c := savedChatWithImage(t, s)
	if err := os.WriteFile(filepath.Join(dir, pinsFile), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if list, err := s.List(); err != nil || len(list) != 1 || list[0].Pinned {
		t.Fatal(list, err)
	}
	if err := s.SetPinned(c.ID, true); err != nil {
		t.Fatal(err)
	}
	future := []byte(`{"version":2,"chat_ids":[]}`)
	if err := os.WriteFile(filepath.Join(dir, pinsFile), future, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPinned(c.ID, false); err == nil {
		t.Fatal("overwrote a newer pins file")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, pinsFile)); string(got) != string(future) {
		t.Fatal("future pins changed")
	}
}
func TestDeleteRemovesDocumentAndImages(t *testing.T) {
	s, _ := Open(t.TempDir())
	c := savedChatWithImage(t, s)
	if err := s.Delete(c.ID); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(s.Dir, "chats", c.ID+".json")) || exists(filepath.Join(s.Dir, "images", c.ID)) {
		t.Fatal("chat files remain")
	}
	entries, _ := os.ReadDir(filepath.Join(s.Dir, "trash"))
	if len(entries) != 0 {
		t.Fatal("trash not emptied", entries)
	}
}
func TestFailedDocumentRemovalKeepsImages(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX directory permissions")
	}
	s, _ := Open(t.TempDir())
	c := savedChatWithImage(t, s)
	chats := filepath.Join(s.Dir, "chats")
	if err := os.Chmod(chats, 0500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(chats, 0700) }()
	if err := s.Delete(c.ID); err == nil {
		t.Fatal("expected failure")
	}
	if !exists(filepath.Join(s.Dir, c.Attachments[0].Path)) {
		t.Fatal("images left without being restored")
	}
	if _, err := s.Load(c.ID); err != nil {
		t.Fatal(err)
	}
}
func TestOpenSettlesInterruptedDeletion(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	kept, gone := savedChatWithImage(t, s), savedChatWithImage(t, s)
	_ = os.MkdirAll(filepath.Join(dir, "trash"), 0700)
	// kept: crashed after moving images; gone: crashed after removing the document.
	if err := os.Rename(filepath.Join(dir, "images", kept.ID), filepath.Join(dir, "trash", kept.ID+".A")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "images", gone.ID), filepath.Join(dir, "trash", gone.ID+".B")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "chats", gone.ID+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dir, kept.Attachments[0].Path)) {
		t.Fatal("images of an existing chat were not restored")
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "trash")); len(entries) != 0 {
		t.Fatal("trash remains", entries)
	}
}
func TestSearchTitlesQuestionsAndAnswers(t *testing.T) {
	s, _ := Open(t.TempDir())
	a := chat.New()
	a.Title = "Собеседование Go"
	a.Messages = []chat.Message{
		{ID: chat.ID(), Role: chat.RoleUser, Text: "Как работает GOMAXPROCS?", Status: chat.StatusComplete, AttachmentIDs: []string{}},
		{ID: chat.ID(), Role: chat.RoleAssistant, Text: "Планировщик использует gomaxprocs потоков.", Status: chat.StatusComplete, AttachmentIDs: []string{}},
	}
	b := chat.New()
	b.Title = "Другое"
	b.Messages = []chat.Message{{ID: chat.ID(), Role: chat.RoleUser, Text: "СОБЕСЕДОВАНИЕ по SQL", Status: chat.StatusComplete, AttachmentIDs: []string{}}}
	for _, c := range []chat.Chat{a, b} {
		if _, err := s.Save(SaveInput{Chat: c}); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := s.Search(context.Background(), "gomaxprocs", 50)
	if err != nil || len(hits) != 1 || hits[0].ChatID != a.ID || hits[0].Matches != 2 {
		t.Fatal(hits, err)
	}
	if hits[0].Snippet != "Как работает GOMAXPROCS?" {
		t.Fatalf("snippet %q", hits[0].Snippet)
	}
	hits, _ = s.Search(context.Background(), "собеседование", 50)
	if len(hits) != 2 {
		t.Fatal(hits)
	}
	if err := s.SetPinned(a.ID, true); err != nil {
		t.Fatal(err)
	}
	hits, _ = s.Search(context.Background(), "собеседование", 1)
	if len(hits) != 1 || hits[0].ChatID != a.ID || !hits[0].Pinned {
		t.Fatal("pinned hit must come first", hits)
	}
	if _, err := s.Search(context.Background(), "  ", 50); err == nil {
		t.Fatal("empty query accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Search(ctx, "go", 50); err == nil {
		t.Fatal("cancelled search returned results")
	}
}
func TestExportWritesMarkdownAndImages(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	c := savedChatWithImage(t, s)
	c.Title = "Go: интервью / раунд 1"
	cfg := c.Config
	cfg.ParallelAnswers = true
	fast, main := chat.ID(), chat.ID()
	c.Messages = []chat.Message{
		{ID: chat.ID(), Role: chat.RoleUser, Text: "Что выведет код?", Status: chat.StatusFailed, AttachmentIDs: []string{c.Attachments[0].ID},
			RequestConfig: &cfg, Attempts: []chat.Attempt{{ID: fast, Status: chat.StatusComplete}, {ID: main, Status: chat.StatusFailed}},
			Answers: []chat.Answer{
				{Kind: chat.AnswerApproach, Status: chat.StatusComplete, Text: "Смотрим на defer.", AttemptID: fast},
				{Kind: chat.AnswerSolution, Status: chat.StatusFailed, Error: "timeout", AttemptID: main},
			}},
	}
	c, err := s.Save(SaveInput{Chat: c})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 22, 12, 30, 0, 0, time.UTC)
	got, err := s.Export(c.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "20261022-123000-Go- интервью - раунд 1" || got.Missing != 0 {
		t.Fatalf("%+v", got)
	}
	md, err := os.ReadFile(filepath.Join(got.Path, "chat.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Go: интервью / раунд 1", "## Вопрос 1", "Что выведет код?", "![Снимок 1](images/01.png)", "### Подход (предварительный)", "Смотрим на defer.", "### Решение", "> Ошибка: timeout"} {
		if !strings.Contains(string(md), want) {
			t.Fatalf("missing %q in\n%s", want, md)
		}
	}
	if png, _ := os.ReadFile(filepath.Join(got.Path, "images", "01.png")); string(png) != "png" {
		t.Fatal("image not copied")
	}
	again, err := s.Export(c.ID, now)
	if err != nil || again.Name != got.Name+"-2" {
		t.Fatal(again, err)
	}
	if p, err := s.ExportPath(got.Name); err != nil || p != got.Path {
		t.Fatal(p, err)
	}
	for _, bad := range []string{"../chats", got.Name + "/images", "chats"} {
		if _, err := s.ExportPath(bad); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, exportsDir)); len(entries) != 2 {
		t.Fatal("temporary export left", entries)
	}
}
func TestExportNotesMissingImage(t *testing.T) {
	s, _ := Open(t.TempDir())
	c := savedChatWithImage(t, s)
	if err := os.Remove(filepath.Join(s.Dir, c.Attachments[0].Path)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Export(c.ID, time.Now())
	if err != nil || got.Missing != 1 {
		t.Fatal(got, err)
	}
	md, _ := os.ReadFile(filepath.Join(got.Path, "chat.md"))
	if !strings.Contains(string(md), "*Снимок 1 недоступен.*") {
		t.Fatal(string(md))
	}
}
