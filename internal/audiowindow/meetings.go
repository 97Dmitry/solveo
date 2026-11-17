package audiowindow

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/97Dmitry/solveo/internal/meeting"
)

type MeetingReader interface {
	MeetingList() ([]meeting.Info, error)
	MeetingDetail(string) (meeting.Document, error)
	OpenMeetingAudio(string, string) (*os.File, error)
}
type MeetingList struct {
	Items  []meeting.Info `json:"items"`
	Notice string         `json:"notice"`
}

func serveMeetings(w http.ResponseWriter, r *http.Request, reader MeetingReader) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if r.URL.Path == "/meetings" {
		items, e := reader.MeetingList()
		result := MeetingList{Items: items}
		if e != nil {
			result.Notice = e.Error()
		}
		_ = json.NewEncoder(w).Encode(result)
		return true
	}
	if strings.HasPrefix(r.URL.Path, "/meeting/") {
		d, e := reader.MeetingDetail(strings.TrimPrefix(r.URL.Path, "/meeting/"))
		if e != nil {
			http.Error(w, "meeting unavailable", http.StatusBadRequest)
		} else {
			_ = json.NewEncoder(w).Encode(d)
		}
		return true
	}
	if strings.HasPrefix(r.URL.Path, "/meeting-audio/") {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/meeting-audio/"), "/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			return true
		}
		f, e := reader.OpenMeetingAudio(parts[0], parts[1])
		if e != nil {
			http.NotFound(w, r)
			return true
		}
		defer func() { _ = f.Close() }()
		info, e := f.Stat()
		if e != nil {
			http.NotFound(w, r)
			return true
		}
		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Minute))
		http.ServeContent(w, r, parts[1], info.ModTime(), f)
		return true
	}
	return false
}
func (c *Client) Meetings() (MeetingList, error) {
	v := MeetingList{Items: []meeting.Info{}}
	e := c.request("/meetings", nil, &v)
	return v, e
}
func (c *Client) Meeting(id string) (meeting.Document, error) {
	var d meeting.Document
	e := c.request("/meeting/"+url.PathEscape(id), nil, &d)
	return d, e
}

// AudioHandler proxies only meeting WAVs; the child never opens archive files.
func (c *Client) AudioHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || !strings.HasPrefix(r.URL.Path, "/meeting-audio/") {
			http.NotFound(w, r)
			return
		}
		req, e := http.NewRequestWithContext(r.Context(), r.Method, c.config.URL+r.URL.EscapedPath(), nil)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		req.Header.Set("Authorization", "Bearer "+c.config.Token)
		if value := r.Header.Get("Range"); value != "" {
			req.Header.Set("Range", value)
		}
		streamClient := *c.http
		streamClient.Timeout = time.Minute
		resp, e := streamClient.Do(req)
		if e != nil {
			http.Error(w, "audio unavailable", http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		for _, name := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Cache-Control", "X-Content-Type-Options"} {
			if v := resp.Header.Get(name); v != "" {
				w.Header().Set(name, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
}
