package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/97Dmitry/solveo/internal/meeting"
)

type annotation struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Speaker string `json:"speaker"`
	Start   string `json:"start_offset"`
	End     string `json:"end_offset"`
}
type content struct {
	Type        string       `json:"type"`
	Text        string       `json:"text"`
	Annotations []annotation `json:"annotations"`
}
type step struct {
	Type    string    `json:"type"`
	Content []content `json:"content"`
}
type interaction struct {
	Status string `json:"status"`
	Steps  []step `json:"steps"`
}
type remoteFile struct {
	Name  string `json:"name"`
	URI   string `json:"uri"`
	State string `json:"state"`
}

func (c *Client) Transcribe(ctx context.Context, in meeting.AudioInput) ([]meeting.Word, error) {
	if in.Duration <= 0 || in.Duration > 30*60 {
		return nil, errors.New("Аудиочасть должна быть не длиннее 30 минут")
	}
	f, e := os.Open(in.Path)
	if e != nil {
		return nil, errors.New("Не удалось открыть аудиочасть")
	}
	defer func() { _ = f.Close() }()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	metadata := strings.NewReader(`{"file":{"display_name":"Solveo meeting part"}}`)
	_, h, e := c.request(ctx, http.MethodPost, "/upload/v1beta/files", metadata, map[string]string{"Content-Type": "application/json", "X-Goog-Upload-Protocol": "resumable", "X-Goog-Upload-Command": "start", "X-Goog-Upload-Header-Content-Length": strconv.FormatInt(info.Size(), 10), "X-Goog-Upload-Header-Content-Type": "audio/wav"})
	if e != nil {
		return nil, e
	}
	uploadURL := h.Get("X-Goog-Upload-URL")
	if uploadURL == "" {
		return nil, errors.New("Gemini не вернул адрес загрузки")
	}
	b, _, e := c.request(ctx, http.MethodPost, uploadURL, f, map[string]string{"Content-Type": "audio/wav", "X-Goog-Upload-Offset": "0", "X-Goog-Upload-Command": "upload, finalize"})
	if e != nil {
		return nil, e
	}
	var uploaded struct {
		File remoteFile `json:"file"`
	}
	if json.Unmarshal(b, &uploaded) != nil || !fileID.MatchString(uploaded.File.Name) {
		return nil, errors.New("Gemini вернул некорректный файл")
	}
	file := uploaded.File
	if e = c.remember(file.Name); e != nil {
		return nil, e
	}
	for file.State == "PROCESSING" {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
		b, _, e = c.request(ctx, http.MethodGet, "/v1beta/"+file.Name, nil, nil)
		if e != nil {
			return nil, e
		}
		if json.Unmarshal(b, &file) != nil {
			return nil, errors.New("Некорректный статус файла Gemini")
		}
	}
	if file.State != "ACTIVE" || file.URI == "" {
		return nil, errors.New("Gemini не подготовил аудиофайл")
	}
	var result interaction
	e = c.json(ctx, "/v1beta/interactions", map[string]any{"model": "gemini-3.5-transcribe", "store": false, "input": []any{map[string]any{"type": "audio", "uri": file.URI, "mime_type": "audio/wav"}}, "generation_config": map[string]any{"transcription_config": map[string]any{"language_codes": []string{}, "mode": map[string]any{"type": "verbatim", "timestamp_granularities": []string{"word"}, "diarization_mode": "speaker"}}}}, &result)
	if e != nil {
		return nil, e
	}
	return parseWords(result, in.Duration)
}
func parseWords(result interaction, duration float64) ([]meeting.Word, error) {
	if result.Status != "completed" {
		return nil, errors.New("Gemini не завершил транскрибацию")
	}
	out := []meeting.Word{}
	hasText := false
	last := float64(0)
	for _, step := range result.Steps {
		if step.Type != "model_output" {
			continue
		}
		for _, c := range step.Content {
			hasText = hasText || strings.TrimSpace(c.Text) != ""
			for _, a := range c.Annotations {
				if a.Type != "word_info" {
					continue
				}
				start, e1 := time.ParseDuration(a.Start)
				end, e2 := time.ParseDuration(a.End)
				if e1 != nil || e2 != nil || start < 0 || end < start || end.Seconds() > duration+.1 || start.Seconds() < last || strings.TrimSpace(a.Text) == "" || len(a.Text) > 65536 || len(a.Speaker) > 128 {
					return nil, errors.New("Gemini вернул некорректные временные метки")
				}
				last = start.Seconds()
				out = append(out, meeting.Word{Text: a.Text, Speaker: a.Speaker, Start: start.Seconds(), End: min(end.Seconds(), duration)})
			}
		}
	}
	if hasText && len(out) == 0 {
		return nil, fmt.Errorf("Gemini вернул текст без запрошенных временных меток")
	}
	return out, nil
}
