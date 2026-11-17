package gemini

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/meeting"
)

func TestRateLimitReturnsSafePauseHint(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"private-key-and-document","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"5s"}]}}`))
	}))
	defer server.Close()
	c := New(func() (string, bool, error) { return "key", true, nil })
	c.BaseURL = server.URL
	_, _, e := c.request(context.Background(), http.MethodPost, "/v1beta/interactions", strings.NewReader(`{}`), nil)
	var limit *meeting.RetryableError
	if !errors.As(e, &limit) || limit.RetryAfter != 5*time.Second || strings.Contains(e.Error(), "private") || requests != 1 {
		t.Fatalf("unsafe rate response: %v requests=%d", e, requests)
	}
}
func TestRateLimitDelayAndDailyQuota(t *testing.T) {
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, header, body string
		delay              time.Duration
		daily              bool
	}{
		{name: "default", delay: time.Minute},
		{name: "http-date", header: now.Add(90 * time.Second).Format(http.TimeFormat), delay: 90 * time.Second},
		{name: "daily", body: `{"error":{"details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateRequestsPerDayPerProjectPerModel"}]}]}}`, daily: true},
		{name: "malformed", header: "invalid", body: `broken`, delay: time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			h.Set("Retry-After", tc.header)
			e := retryableError(http.StatusTooManyRequests, h, []byte(tc.body), now)
			if e.RetryAfter != tc.delay || e.Daily != tc.daily {
				t.Fatalf("%+v", e)
			}
		})
	}
}

func TestOtherHTTPFailuresAreNotQuotaPauses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "private", http.StatusForbidden) }))
	defer server.Close()
	c := New(func() (string, bool, error) { return "key", true, nil })
	c.BaseURL = server.URL
	_, _, e := c.request(context.Background(), http.MethodGet, "/v1beta/models", nil, nil)
	var limit *meeting.RetryableError
	if e == nil || errors.As(e, &limit) || strings.Contains(e.Error(), "private") {
		t.Fatalf("incorrect error %v", e)
	}
}

func TestServiceUnavailableReturnsResumeHint(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Retry-After", "7")
		http.Error(w, "private input", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	c := New(func() (string, bool, error) { return "key", true, nil })
	c.BaseURL = server.URL
	_, _, e := c.request(context.Background(), http.MethodPost, "/v1beta/interactions", strings.NewReader(`{}`), nil)
	var pause *meeting.RetryableError
	if !errors.As(e, &pause) || pause.StatusCode != 503 || pause.RetryAfter != 7*time.Second || strings.Contains(e.Error(), "ключ") || strings.Contains(e.Error(), "private") || requests != 1 {
		t.Fatalf("bad 503 result %v requests=%d", e, requests)
	}
}
