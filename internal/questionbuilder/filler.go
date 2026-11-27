package questionbuilder

import (
	"regexp"
	"strings"
	"unicode"
)

var asrMarker = regexp.MustCompile(`\[[^\[\]]*\]|\([^()]*\)|<[^<>]*>`)

// IsFiller recognizes ASR placeholders and unambiguous hesitations or acknowledgments.
// Keep short replies like "да" and "нет" because they can change the answer.
func IsFiller(text string) bool {
	text = asrMarker.ReplaceAllStringFunc(text, func(marker string) string {
		switch strings.ToLower(strings.TrimSpace(marker[1 : len(marker)-1])) {
		case "тишина", "пауза", "шум", "музыка", "смех", "кашель", "вздох", "неразборчиво",
			"silence", "pause", "noise", "music", "laughter", "laughs", "cough", "sigh", "inaudible", "unintelligible":
			return " "
		}
		return marker
	})
	words := strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	})
	for _, word := range words {
		switch strings.ToLower(word) {
		case "э", "ээ", "эээ", "эм", "мм", "ммм", "мгм", "угу", "ага", "ок",
			"uh", "um", "erm", "hm", "hmm", "mm", "mmm", "mhm", "huh", "ok", "okay":
			continue
		default:
			return false
		}
	}
	return true
}
