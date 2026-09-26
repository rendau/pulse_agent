package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMask(t *testing.T) {
	s := New(Config{Key: []byte("k")})

	token := s.tokenize("phone", "+7 (701) 123-45-67")
	assert.Regexp(t, `^pii:phone:[a-p]{12}$`, token)
	assert.Equal(t, token, s.tokenize("phone", "87011234567"), "одно значение в разном написании — один токен")
	assert.Equal(t, token, s.tokenize("phone", "7011234567"))
	assert.NotEqual(t, token, s.tokenize("phone", "+7 701 123 45 68"))
	other := New(Config{Key: []byte("другой ключ")})
	assert.NotEqual(t, token, other.tokenize("phone", "87011234567"), "без ключа не подобрать")

	text := s.Mask(`что с клиентом +7 701 123 45 67? почта Ivan@Mail.kz, карта 4111 1111 1111 1111`)
	assert.Contains(t, text, token)
	assert.Contains(t, text, s.tokenize("email", "ivan@mail.kz"))
	assert.Contains(t, text, "***1111")
	assert.NotContains(t, text, "701 123")
	assert.Equal(t, text, s.Mask(text), "повторная обработка ничего не меняет")
	assert.Contains(t, s.Mask(`{"phone":"8 701 123 45 67"}`), token, "значение явного поля — целиком")
}

func TestMaskToolOutput(t *testing.T) {
	s := New(Config{Key: []byte("k")})

	out := s.MaskToolOutput(`{"service":"orders","data":{"customer_phone":"8 701 123 45 67","customer_id":42,` +
		`"customer_name":"Иван Петров","note":"звонил с +77011234567 <срочно>",` +
		`"history":[{"email":"a@b.kz"},{"email":"c@d.kz"}]},` +
		`"personal_fields":{"customer_phone":"phone","customer_id":"customer_id","customer_name":"name","history[].email":"email"}}`)

	phone := s.tokenize("phone", "87011234567")
	assert.Contains(t, out, `"customer_phone":"`+phone+`"`)
	assert.Contains(t, out, `"customer_id":"`+s.tokenize("customer_id", "42")+`"`, "число — тоже токеном")
	assert.Contains(t, out, `"customer_name":"`+s.tokenize("name", "Иван Петров")+`"`, "имя — только по отметке сервиса")
	assert.Contains(t, out, "звонил с "+phone+" <срочно>", "телефон в тексте; < > без экранирования")
	assert.Contains(t, out, s.tokenize("email", "c@d.kz"), "путь через массив")
	assert.NotContains(t, out, "Иван")
	assert.NotContains(t, out, "701")

	assert.Equal(t, "ERROR: нет ответа, звонок на "+phone, s.MaskToolOutput("ERROR: нет ответа, звонок на +7 701 123 45 67"), "не JSON — как текст")
	assert.JSONEq(t, `{"value":12345678901234567890}`, s.MaskToolOutput(`{"value":12345678901234567890}`), "большие числа без потерь")
}

func TestReveal(t *testing.T) {
	s := New(Config{Key: []byte("k")})
	phone := s.tokenize("phone", "8 701 123 45 67")
	name := s.tokenize("name", "Иван \"Ваня\" Петров")

	args := s.RevealArgs(`{"pattern":"` + phone + `","window":"24h"}`)
	assert.JSONEq(t, `{"pattern":"+77011234567","window":"24h"}`, args, "телефон — с «+»: pulse ищет его в любом написании")
	assert.JSONEq(t, `{"pattern":"`+name+`"}`, s.RevealArgs(`{"pattern":"`+name+`"}`), "имя в поиск не раскрывается")
	unknown := "pii:phone:abcdefghijkl"
	assert.JSONEq(t, `{"pattern":"`+unknown+`"}`, s.RevealArgs(`{"pattern":"`+unknown+`"}`), "неизвестный токен — как есть")

	assert.Equal(t, "Клиент +77011234567 (Иван \"Ваня\" Петров), заказ 234115", s.Reveal("Клиент "+phone+" ("+name+"), заказ 234115"))

	answer := s.Reveal(`{"summary":"клиент ` + name + `","facts":[]}`)
	assert.JSONEq(t, `{"summary":"клиент Иван \"Ваня\" Петров","facts":[]}`, answer, "в JSON — по строкам, кавычки не ломают JSON")

	assert.Equal(t, "без токенов {", s.Reveal("без токенов {"))
}

func TestVaultLimit(t *testing.T) {
	s := New(Config{Key: []byte("k"), MaxEntries: 2})
	first := s.tokenize("phone", "87011234567")
	s.tokenize("phone", "87011234568")
	s.tokenize("phone", "87011234569")
	require.Len(t, s.vault, 2)
	assert.Equal(t, first, s.Reveal(first), "самый старый забыт")
}
