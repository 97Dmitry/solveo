package audiowindow

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	core "github.com/97Dmitry/solveo/internal/app"
	"github.com/97Dmitry/solveo/internal/meeting"
)

type meetingReader struct{}

func (meetingReader) LoadChatAudioHistory(string) (core.AudioHistoryView, error) {
	return core.AudioHistoryView{}, nil
}
func (meetingReader) MeetingList() ([]meeting.Info, error) {
	return []meeting.Info{{ID: "test", Title: "Weekly"}}, nil
}
func (meetingReader) MeetingDetail(string) (meeting.Document, error) {
	return meeting.Document{Info: meeting.Info{Title: "Weekly"}}, nil
}
func (meetingReader) OpenMeetingAudio(string, string) (*os.File, error) { return nil, os.ErrNotExist }
func TestMeetingIPC(t *testing.T) {
	s, e := New(func() View { return View{} }, func(context.Context, Command) error { return nil }, meetingReader{})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	c, e := NewClient(s.Config())
	if e != nil {
		t.Fatal(e)
	}
	list, e := c.Meetings()
	if e != nil || len(list.Items) != 1 {
		t.Fatalf("%+v %v", list, e)
	}
	if !allowed("meeting") {
		t.Fatal("meeting command not allowlisted")
	}
	response, e := http.Get(s.Config().URL + "/meetings")
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 403 {
		t.Fatal("unauthenticated list accepted")
	}
}

type storedMeetingReader struct{ store *meeting.Store }

func (storedMeetingReader) LoadChatAudioHistory(string) (core.AudioHistoryView, error) {
	return core.AudioHistoryView{}, nil
}
func (r storedMeetingReader) MeetingList() ([]meeting.Info, error) { return r.store.List() }
func (r storedMeetingReader) MeetingDetail(id string) (meeting.Document, error) {
	return r.store.Load(id)
}
func (r storedMeetingReader) OpenMeetingAudio(id, file string) (*os.File, error) {
	return r.store.OpenAudio(id, file)
}
func TestMeetingAudioRangeAndPathConfinement(t *testing.T) {
	dir := t.TempDir()
	store := meeting.NewStore(dir)
	d, e := store.New("Weekly", "", []string{"mic"})
	if e != nil {
		t.Fatal(e)
	}
	d.Parts = []meeting.Part{{File: "mic-0000.wav", Track: "mic", Rate: 16000, Samples: 1, Complete: true}}
	if e = store.Save(d); e != nil {
		t.Fatal(e)
	}
	data := make([]byte, 46)
	data[44] = 12
	if e = os.WriteFile(filepath.Join(dir, d.ID, "mic-0000.wav"), data, 0600); e != nil {
		t.Fatal(e)
	}
	server, e := New(func() View { return View{} }, func(context.Context, Command) error { return nil }, storedMeetingReader{store})
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	req, e := http.NewRequest(http.MethodGet, server.Config().URL+"/meeting-audio/"+d.ID+"/mic-0000.wav", nil)
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Authorization", "Bearer "+server.Config().Token)
	req.Header.Set("Range", "bytes=44-45")
	response, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	b, e := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if e != nil || response.StatusCode != 206 || len(b) != 2 || b[0] != 12 {
		t.Fatalf("range %d %v %v", response.StatusCode, b, e)
	}
	outside := filepath.Join(t.TempDir(), "outside.wav")
	if e = os.WriteFile(outside, data, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(dir, d.ID, "mic-0000.wav")); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(outside, filepath.Join(dir, d.ID, "mic-0000.wav")); e != nil {
		t.Skip("symlinks unavailable")
	}
	if f, e := store.OpenAudio(d.ID, "mic-0000.wav"); e == nil {
		_ = f.Close()
		t.Fatal("audio escaped archive")
	}
}
