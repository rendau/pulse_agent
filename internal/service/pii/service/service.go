// Package service — токены персональных данных: HMAC значения, приведённого к одному виду
// (телефон: цифры с кодом страны), и память «токен → значение» для запросов к pulse и ответа.
package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"
)

const (
	// tokenChars — длина кода токена: 12 букв a–p (48 бит). Букв, а не цифр: токен не должен
	// быть похож на телефон или номер карты при повторной обработке текста
	tokenChars = 12
	prefix     = "pii:"
	mask       = "***"

	kindPhone = "phone"
	kindEmail = "email"
	kindCard  = "card"
)

// виды, по токену которых модель может искать дальше: только они раскрываются в запросах к pulse
var searchable = []string{"phone", "email", "iin", "customer_id"}

// tokenRe — токен в тексте.
var tokenRe = regexp.MustCompile(`pii:(phone|email|iin|customer_id|name|address|document|other):[a-p]{12}`)

type Config struct {
	// Key — ключ HMAC (секрет); пусто — случайный на время жизни процесса (токены не
	// совпадут после рестарта)
	Key []byte
	// CountryCode — код страны для телефонов: 8… и 10 цифр приводятся к нему
	CountryCode string
	// Ttl — сколько помнить значение токена; MaxEntries — потолок памяти
	Ttl        time.Duration
	MaxEntries int
}

type Service struct {
	conf Config

	mu    sync.Mutex
	vault map[string]entry
}

type entry struct {
	kind    string
	value   string // приведённое к одному виду — для запросов к pulse
	display string // для человека
	at      time.Time
}

func New(conf Config) *Service {
	if len(conf.Key) == 0 {
		conf.Key = make([]byte, 32)
		_, _ = rand.Read(conf.Key)
		slog.Warn("PII_TOKEN_KEY is empty: personal data tokens are random per process and change after restart")
	}
	if conf.CountryCode == "" {
		conf.CountryCode = "7"
	}
	if conf.Ttl <= 0 {
		conf.Ttl = 24 * time.Hour
	}
	if conf.MaxEntries <= 0 {
		conf.MaxEntries = 100_000
	}
	return &Service{conf: conf, vault: map[string]entry{}}
}

func (s *Service) Mask(text string) string {
	// готовые токены не трогаются: иначе правило явного поля (phone: …) найдёт «phone:»
	// внутри самого токена
	var b strings.Builder
	last := 0
	for _, loc := range tokenRe.FindAllStringIndex(text, -1) {
		b.WriteString(replacePII(text[last:loc[0]], s.tokenize))
		b.WriteString(text[loc[0]:loc[1]])
		last = loc[1]
	}
	b.WriteString(replacePII(text[last:], s.tokenize))
	return b.String()
}

func (s *Service) MaskToolOutput(text string) string {
	data, ok := decode(text)
	if !ok {
		return s.Mask(text)
	}

	// call_service_endpoint: персональные поля ответа ручки — по виду, объявленному сервисом
	if obj, ok := data.(map[string]any); ok {
		if fields, ok := obj["personal_fields"].(map[string]any); ok {
			for path, kind := range fields {
				if kind, ok := kind.(string); ok {
					obj["data"] = s.maskPath(obj["data"], strings.Split(path, "."), kind)
				}
			}
		}
	}

	return encode(walkStrings(data, s.Mask))
}

func (s *Service) RevealArgs(args string) string {
	reveal := func(text string) string {
		return tokenRe.ReplaceAllStringFunc(text, func(token string) string {
			e, ok := s.lookup(token)
			if !ok || !slices.Contains(searchable, e.kind) {
				return token
			}
			// телефон — с «+»: так pulse узнаёт номер и ищет его в любом написании
			return lo.Ternary(e.kind == kindPhone, "+"+e.value, e.value)
		})
	}
	data, ok := decode(args)
	if !ok {
		return reveal(args)
	}
	return encode(walkStrings(data, reveal))
}

func (s *Service) Reveal(text string) string {
	reveal := func(text string) string {
		return tokenRe.ReplaceAllStringFunc(text, func(token string) string {
			if e, ok := s.lookup(token); ok {
				return e.display
			}
			return token
		})
	}
	if !tokenRe.MatchString(text) {
		return text
	}
	data, ok := decode(text)
	if !ok {
		return reveal(text)
	}
	return encode(walkStrings(data, reveal))
}

// maskPath — значение по пути personal_fields (history[].phone; [] — элементы массива,
// {} — значения словаря) токеном вида kind.
func (s *Service) maskPath(v any, path []string, kind string) any {
	if len(path) == 0 {
		switch value := v.(type) {
		case string:
			return s.tokenize(kind, value)
		case json.Number:
			return s.tokenize(kind, value.String())
		default:
			return v
		}
	}

	seg, rest := path[0], path[1:]
	if seg == "{}" {
		if obj, ok := v.(map[string]any); ok {
			for k, child := range obj {
				obj[k] = s.maskPath(child, rest, kind)
			}
		}
		return v
	}

	name, isArray := strings.CutSuffix(seg, "[]")
	if name != "" {
		obj, ok := v.(map[string]any)
		if !ok {
			return v
		}
		child, ok := obj[name]
		if !ok {
			return v
		}
		if isArray {
			obj[name] = s.maskItems(child, rest, kind)
		} else {
			obj[name] = s.maskPath(child, rest, kind)
		}
		return v
	}
	return s.maskItems(v, rest, kind)
}

func (s *Service) maskItems(v any, rest []string, kind string) any {
	arr, ok := v.([]any)
	if !ok {
		return v
	}
	for i := range arr {
		arr[i] = s.maskPath(arr[i], rest, kind)
	}
	return arr
}

// tokenize — токен значения вида kind; номер карты — только маска с последними цифрами.
func (s *Service) tokenize(kind, value string) string {
	value = strings.TrimSpace(value)
	if value == "" || tokenRe.MatchString(value) || value == mask || strings.HasPrefix(value, mask) {
		return value
	}
	if kind == kindCard {
		return maskCard(value)
	}
	normalized, display, ok := s.normalize(kind, value)
	if !ok {
		return mask
	}

	mac := hmac.New(sha256.New, s.conf.Key)
	mac.Write([]byte(kind + ":" + normalized))
	sum := mac.Sum(nil)
	code := make([]byte, tokenChars)
	for i := range code {
		code[i] = 'a' + (sum[i/2]>>(4*(1-i%2)))&0x0f
	}
	token := prefix + kind + ":" + string(code)

	s.remember(token, entry{kind: kind, value: normalized, display: display})
	return token
}

// normalize — значение одного вида (иначе +7 701…, 8701… и 7701… дали бы разные токены) и
// его вид для человека.
func (s *Service) normalize(kind, value string) (normalized, display string, ok bool) {
	switch kind {
	case kindPhone:
		d := digits(value)
		switch {
		case len(d) == 10:
			d = s.conf.CountryCode + d
		case len(d) == 11 && d[0] == '8' && s.conf.CountryCode == "7":
			d = "7" + d[1:]
		}
		return d, "+" + d, len(d) >= 10 && len(d) <= 15
	case kindEmail:
		v := strings.ToLower(value)
		return v, v, strings.Count(v, "@") == 1 && !strings.ContainsAny(v, " \t")
	case "iin":
		d := digits(value)
		return d, d, len(d) >= 6
	case "name":
		return strings.Join(strings.Fields(strings.ToLower(value)), " "), value, true
	default:
		return value, value, true
	}
}

func (s *Service) lookup(token string) (entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.vault[token]
	if !ok || time.Since(e.at) > s.conf.Ttl {
		return entry{}, false
	}
	return e, true
}

func (s *Service) remember(token string, e entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.at = time.Now()
	s.vault[token] = e
	if len(s.vault) <= s.conf.MaxEntries {
		return
	}
	// переполнение: сначала устаревшие, затем самые старые
	for t, v := range s.vault {
		if time.Since(v.at) > s.conf.Ttl {
			delete(s.vault, t)
		}
	}
	for len(s.vault) > s.conf.MaxEntries {
		oldest, at := "", time.Now()
		for t, v := range s.vault {
			if v.at.Before(at) {
				oldest, at = t, v.at
			}
		}
		delete(s.vault, oldest)
	}
}

func maskCard(value string) string {
	d := digits(value)
	if len(d) < 4 {
		return mask
	}
	return mask + d[len(d)-4:]
}

// decode — JSON-объект или массив (числа — как есть, без потери точности); иначе ok = false.
func decode(text string) (any, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber()
	var data any
	if err := dec.Decode(&data); err != nil || dec.More() {
		return nil, false
	}
	return data, true
}

// encode — JSON без экранирования <, >, & (модель и человек читают его как есть).
func encode(data any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(data)
	return strings.TrimSuffix(b.String(), "\n")
}

// walkStrings применяет f ко всем строкам JSON, включая ключи объектов.
func walkStrings(v any, f func(string) string) any {
	switch value := v.(type) {
	case string:
		return f(value)
	case []any:
		for i := range value {
			value[i] = walkStrings(value[i], f)
		}
		return value
	case map[string]any:
		result := make(map[string]any, len(value))
		for k, child := range value {
			result[f(k)] = walkStrings(child, f)
		}
		return result
	default:
		return v
	}
}
