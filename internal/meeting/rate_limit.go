package meeting

import "time"

// RetryableError is an explicit HTTP 429 or 503 response eligible for manual continuation.
// It never implies an uncertain transport outcome or an invalid credential.
type RetryableError struct {
	StatusCode int
	RetryAfter time.Duration
	Daily      bool
}

func (e *RetryableError) Error() string {
	if e.StatusCode == 503 {
		return "Gemini временно недоступен (HTTP 503). Подождите и продолжите обработку."
	}
	if e.Daily {
		return "Gemini сообщил о суточном лимите запросов. Продолжите после сброса квоты."
	}
	return "Gemini сообщил о лимите запросов (HTTP 429). Подождите и продолжите обработку."
}

type PausedOperation struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Partial bool   `json:"partial"`
	RetryAt string `json:"retry_at"`
	Reason  string `json:"reason"`
}
