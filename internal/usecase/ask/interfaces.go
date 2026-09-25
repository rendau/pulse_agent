package ask

import (
	"context"

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

type AgentI interface {
	Run(ctx context.Context, req *agentModel.Req) (*agentModel.Result, error)
}
