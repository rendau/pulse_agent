package model

import "time"

// Info — что за агент работает: версия, модель, лимиты, клиенты, инструменты pulse.
type Info struct {
	Version         string
	StartedAt       time.Time
	LlmProvider     string
	LlmModel        string
	ReasoningEffort string
	MaxToolCalls    int
	Timeout         time.Duration
	Clients         []string // системы с ключами API_KEYS
	EvalClients     []string // системы, которым можно запускать прогон
	EvalCases       []string
	// Journal — где журнал вопросов: postgres | memory; JournalRetention — срок хранения
	// (0 — без срока: журнал в памяти, последние N)
	Journal          string
	JournalRetention time.Duration
	// PulseTools — инструменты pulse (каталог перечитывается на каждый разбор); PulseError —
	// pulse не ответил
	PulseTools []string
	PulseError string
}
