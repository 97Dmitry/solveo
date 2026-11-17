package app

import (
	"context"
	"testing"

	"github.com/97Dmitry/solveo/internal/audio"
	"github.com/97Dmitry/solveo/internal/meeting"
)

type meetingCapture struct{}

func (meetingCapture) Start(context.Context, audio.Source, func(audio.Frame), func(error)) (audio.Device, func(), error) {
	return audio.Device{}, func() {}, nil
}
func TestMeetingOwnsCaptureAndOriginalChat(t *testing.T) {
	c, _, _ := setup(t)
	c.deps.Meetings = meeting.NewManager(meeting.NewStore(t.TempDir()), meetingCapture{}, nil)
	chatID := c.Snapshot().ActiveChatID
	if e := c.MeetingCommand(MeetingCommand{Action: "start", Title: "Weekly", Tracks: []string{"mic"}}); e != nil {
		t.Fatal(e)
	}
	id := c.MeetingState().RecordingID
	if e := c.StartListening(); e == nil {
		t.Fatal("live capture started during meeting")
	}
	if _, e := c.NewChat(); e != nil {
		t.Fatal(e)
	}
	d, e := c.MeetingDetail(id)
	if e != nil || d.ChatID != chatID {
		t.Fatalf("meeting changed owner %+v %v", d, e)
	}
	if e = c.MeetingCommand(MeetingCommand{Action: "stop"}); e != nil {
		t.Fatal(e)
	}
}
