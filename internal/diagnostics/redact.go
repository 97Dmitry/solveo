package diagnostics

import (
	"regexp"
	"strings"
	"unicode"
)

// Hidden replaces a masked secret.
const Hidden = "[скрыто]"

var (
	bearerPattern = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`)
	fieldPattern  = regexp.MustCompile(`(?i)\b(api[_-]?key|x-goog-api-key|access[_-]?token|refresh[_-]?token|id[_-]?token|token|secret|password|authorization)("?\s*[=:]\s*"?)[^\s"'&,;}]{4,}`)
	keyPattern    = regexp.MustCompile(`\b(?:xai-[A-Za-z0-9]{16,}|AIza[0-9A-Za-z_-]{30,}|sk-[A-Za-z0-9_-]{16,}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,})`)
	// Long runs of letters and digits look like keys, tokens or encoded data; chat IDs (32 hex) stay readable.
	blobPattern = regexp.MustCompile(`[A-Za-z0-9+_=-]{40,}`)
)

// Redact masks values that look like credentials. It is a second line of
// defense: callers must not pass secrets or private content in the first place.
func Redact(s string) string {
	s = bearerPattern.ReplaceAllString(s, "Bearer "+Hidden)
	s = fieldPattern.ReplaceAllString(s, "${1}${2}"+Hidden)
	s = keyPattern.ReplaceAllString(s, Hidden)
	return blobPattern.ReplaceAllStringFunc(s, func(m string) string {
		if strings.ContainsAny(m, "0123456789") && strings.ContainsFunc(m, unicode.IsLetter) {
			return Hidden
		}
		return m
	})
}
