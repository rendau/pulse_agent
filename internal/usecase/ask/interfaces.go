package ask

import (
	"context"

	chatModel "github.com/mechta-market/pulse_agent/internal/domain/chat/model"
	dialogModel "github.com/mechta-market/pulse_agent/internal/domain/dialog/model"
	journalModel "github.com/mechta-market/pulse_agent/internal/domain/journal/model"
	agentModel "github.com/mechta-market/pulse_agent/internal/service/agent/model"
)

type DialogServiceI interface {
	History(ctx context.Context, conversationId string) ([]*dialogModel.Turn, error)
	Append(ctx context.Context, conversationId string, question, answer string) error
	Reset(ctx context.Context, conversationId string) error
}

type JournalServiceI interface {
	Append(ctx context.Context, e *journalModel.Entry) error
}

// PiiI — персональные данные токенами: журнал и история беседы хранят то, что видела модель.
type PiiI interface {
	Mask(text string) string
}

type AgentI interface {
	Run(ctx context.Context, req *agentModel.Req) (*agentModel.Result, error)
}

// ChatServiceI — контекст беседы (заметки); nil — без хранилища: беседы без заметок и инструментов.
type ChatServiceI interface {
	Get(ctx context.Context, client, conversationId string) (*chatModel.Chat, error)
}
