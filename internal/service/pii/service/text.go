package service

import (
	"regexp"
	"strings"
)

// Правила поиска PII в свободном тексте — те же, что у pulse (internal/util/redact): грубые
// намеренно — лишний токен дешевле номера, ушедшего к LLM-провайдеру.
var (
	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	// карта: 13–19 цифр, допускаются пробелы и дефисы между группами; только если проходит
	// проверку Луна — длинные идентификаторы заказов не трогаются
	cardRe = regexp.MustCompile(`\b\d(?:[ -]?\d){12,18}\b`)
	// телефон: международный с «+» (10–15 цифр) или 11 цифр с 7/8 в начале (KZ/RU),
	// с пробелами, дефисами и скобками между группами
	phoneRe = regexp.MustCompile(`\+\d(?:[ ()-]*\d){9,14}\b|\b[78](?:[ ()-]*\d){10}\b`)
	// значение явного поля phone/email/card — целиком, в каком бы формате ни было
	// (значение в кавычках — целиком, без кавычек — с группами цифр через пробел: «+7 701 123 45 67»)
	piiFieldRe = regexp.MustCompile(`(?i)("?(?:phone|tel|msisdn|email|e-mail|card|pan)(?:_?(?:number|num|no))?"?\s*[:=]\s*)("[^"]*"|[^",\s}]+(?:[ ()-]+\d+)*)`)
)

// replacePII заменяет персональные данные в тексте тем, что вернёт replace (kind — phone,
// email или card).
func replacePII(s string, replace func(kind, value string) string) string {
	s = piiFieldRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := piiFieldRe.FindStringSubmatch(m)
		name := strings.ToLower(sub[1])
		kind := kindPhone
		switch {
		case strings.Contains(name, "mail"):
			kind = kindEmail
		case strings.Contains(name, "card"), strings.Contains(name, "pan"):
			kind = kindCard
		}
		if value, ok := strings.CutPrefix(sub[2], `"`); ok {
			return sub[1] + `"` + replace(kind, strings.TrimSuffix(value, `"`)) + `"`
		}
		return sub[1] + replace(kind, sub[2])
	})
	s = emailRe.ReplaceAllStringFunc(s, func(m string) string { return replace(kindEmail, m) })
	s = cardRe.ReplaceAllStringFunc(s, func(m string) string {
		if luhn(digits(m)) {
			return replace(kindCard, m)
		}
		return m
	})
	return phoneRe.ReplaceAllStringFunc(s, func(m string) string { return replace(kindPhone, m) })
}

func digits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

func luhn(number string) bool {
	sum, double := 0, false
	for i := len(number) - 1; i >= 0; i-- {
		d := int(number[i] - '0')
		if double {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return len(number) >= 13 && sum%10 == 0
}
