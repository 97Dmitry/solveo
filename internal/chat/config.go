package chat

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// MaxConfigText limits each free-text preparation field in bytes.
const MaxConfigText = 1 << 20

func ValidateConfig(c Config) error {
	for _, v := range []string{c.Position, c.Stack, c.InterviewFormat, c.Experience, c.Context, c.Instructions, c.Model, c.FastModel, c.ResponseLanguage, c.CodeLanguage} {
		if len(v) > MaxConfigText || !utf8.ValidString(v) || strings.ContainsRune(v, 0) {
			return errors.New("некорректная преднастройка чата: поле длиннее 1 МиБ, содержит NUL или некорректный UTF-8")
		}
	}
	if !ValidReasoning(c.Reasoning) || !ValidReasoning(c.FastReasoning) {
		return errors.New("неподдерживаемый уровень рассуждения")
	}
	switch c.Detail {
	case "brief", "normal", "detailed":
	default:
		return errors.New("некорректная подробность ответа")
	}
	return nil
}

// ValidReasoning accepts provider-advertised effort tokens, including defaults.
func ValidReasoning(value string) bool {
	if len(value) > 64 {
		return false
	}
	for _, c := range value {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}
