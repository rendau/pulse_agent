package model

import (
	agentModel "github.com/rendau/pulse_agent/internal/service/agent/model"
	llmModel "github.com/rendau/pulse_agent/internal/service/llm/model"
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
	// ResponseSchema — JSON Schema ответа от клиента (RPC); задана — формат json
	ResponseSchema map[string]any
}

type User struct {
	Id   string
	Name string
}

// Answer — ответ. Incomplete — почему разбор закончен досрочно
// (agent/model.Incomplete*); пусто — ответ полный.
type Answer struct {
	Text string
	// ModelAnswer — ответ, как его написала модель (персональные данные токенами): журнал
	ModelAnswer string
	Structured  *agentModel.Structured
	// Json — ответ в JSON (формат json или схема клиента); nil — текстовый формат
	Json       []byte
	Incomplete string
	Charts     []agentModel.Chart

	Steps     int
	ToolCalls int
	Usage     llmModel.Usage
	Trace     []agentModel.ToolTrace
}
