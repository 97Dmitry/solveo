package gemini

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/97Dmitry/solveo/internal/meeting"
)

// Only structured retry/quota fields are inspected. Vendor text can contain
// private input or credentials and must never reach the UI or logs.
func retryableError(statusCode int, header http.Header, body []byte, now time.Time) *meeting.RetryableError {
	e := &meeting.RetryableError{StatusCode: statusCode}
	value := header.Get("Retry-After")
	if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds >= 0 {
		e.RetryAfter = time.Duration(seconds) * time.Second
	} else if at, err := http.ParseTime(value); err == nil {
		e.RetryAfter = max(0, at.Sub(now))
	}
	var response struct {
		Error struct {
			Details []struct {
				Type       string `json:"@type"`
				RetryDelay string `json:"retryDelay"`
				Violations []struct {
					QuotaID     string `json:"quotaId"`
					QuotaMetric string `json:"quotaMetric"`
				} `json:"violations"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &response) == nil {
		for _, d := range response.Error.Details {
			switch d.Type {
			case "type.googleapis.com/google.rpc.RetryInfo":
				if delay, err := time.ParseDuration(d.RetryDelay); err == nil && delay >= 0 {
					e.RetryAfter = max(e.RetryAfter, delay)
				}
			case "type.googleapis.com/google.rpc.QuotaFailure":
				for _, v := range d.Violations {
					s := strings.ToLower(v.QuotaID + " " + v.QuotaMetric)
					e.Daily = statusCode == http.StatusTooManyRequests && (e.Daily || strings.Contains(s, "perday") || strings.Contains(s, "per_day") || strings.Contains(s, "daily"))
				}
			}
		}
	}
	if e.RetryAfter == 0 && !e.Daily {
		e.RetryAfter = time.Minute
		if statusCode == http.StatusServiceUnavailable {
			e.RetryAfter = 15 * time.Second
		}
	}
	return e
}
