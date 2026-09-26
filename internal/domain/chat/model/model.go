package model

import "time"

// MaxNotesChars — заметки беседы идут модели с каждым вопросом: длиннее — дорого и размывает промпт.
const MaxNotesChars = 2000

// Chat — контекст беседы системы-клиента (чат Telegram, тикет): что попросили запомнить и
// докуда беседа получила уведомления. Ключ — клиент и его conversation_id: беседы разных
// систем не пересекаются.
type Chat struct {
	Client         string
	ConversationId string

	// Notes — заметки беседы (как CLAUDE.md): команда, какие сервисы интересуют, как отвечать
	Notes          string
	NotesUpdatedAt *time.Time
	NotesUpdatedBy string

	// NotifyCursor — id последнего уведомления, которое беседа получила; nil — ещё не читала ленту
	NotifyCursor *int64
}
