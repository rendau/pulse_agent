package model

import (
	agentModel "github.com/mechta-market/pulse_agent/internal/service/agent/model"
	llmModel "github.com/mechta-market/pulse_agent/internal/service/llm/model"
)

// Question — вопрос системы-клиента.
type Question struct {
	// Client — система, задавшая вопрос (по ключу API): журнал и метрики
	Client string
	// ConversationId — беседа клиента: своя история; пусто — вопрос без истории
	ConversationId string
	// User — кто спросил в системе клиента (журнал; позже — доступ к данным)
	User User
	Text string
	// Format — оформление ответа (agent/service/constant.Format*); пусто — telegram
	Format string
	// Charts — клиент принимает графики
	Charts bool
}

type User struct {
	Id   string
	Name string
}

// Answer — ответ. Incomplete — почему разбор закончен досрочно
// (agent/model.Incomplete*); пусто — ответ полный.
type Answer struct {
	Text       string
	Structured *agentModel.Structured
	Incomplete string
	Charts     []agentModel.Chart

	Steps     int
	ToolCalls int
	Usage     llmModel.Usage
	Trace     []agentModel.ToolTrace
}
