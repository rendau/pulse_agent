// Package model — внешняя находка об атаке (POST /v1/findings) и итог её приёма.
package model

// Finding — находка системы-клиента (расписание Claude через pulse): уже разобрана, уходит в
// ленту как есть, без модели.
type Finding struct {
	// Key — «security:<адрес>»: дедупликация по адресу вместе с сигналами наблюдателя
	Key      string
	Kind     string // пока только security
	Severity string // warning | critical
	Title    string
	Text     string
	// Source — кто нашёл (claude-routine — расписание Claude)
	Source string
}

// Result — id уведомления (0 — не создано: о том же адресе уже сообщали).
type Result struct {
	Id           int64
	Deduplicated bool
}
