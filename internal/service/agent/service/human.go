package service

import (
	"encoding/json"
	"fmt"

	agentModel "github.com/rendau/pulse_agent/internal/service/agent/model"
	localConstant "github.com/rendau/pulse_agent/internal/service/agent/service/constant"
)

// screenResult — единственная проверка ответа pulse перед моделью (fail-closed): у ответа с
// audience: human на верхнем уровне (pulse ставит его ответам ручек для человека) — human: клиенту
// как есть, модели — только отметка; неизвестный audience или неразобранный ответ
// call_service_endpoint — blocked: не передаётся никому. Остальное — модели обычным путём.
// Проверяется ответ любого инструмента, а не только call_service_endpoint. args — аргументы после
// подстановки токенов (параметры вызова для человека — настоящими значениями).
func screenResult(tool, text, args string) (human *agentModel.HumanReply, blocked bool) {
	var rep struct {
		Service    string          `json:"service"`
		EndpointId string          `json:"endpoint_id"`
		Title      string          `json:"title"`
		Audience   *string         `json:"audience"`
		StatusCode int             `json:"status_code"`
		Data       json.RawMessage `json:"data"`
		Rows       int             `json:"rows"`
		TotalRows  int             `json:"total_rows"`
		Truncated  bool            `json:"truncated"`
		RequestId  string          `json:"request_id"`
		Masked     int             `json:"masked_fields"`
	}
	if err := json.Unmarshal([]byte(text), &rep); err != nil {
		// ответ ручки всегда JSON-объект: иначе — не угадываем, чей он
		return nil, tool == localConstant.EndpointTool
	}
	switch {
	case rep.Audience == nil || *rep.Audience == "":
		return nil, false
	case *rep.Audience != localConstant.AudienceHuman:
		return nil, true
	}

	var call struct {
		Params map[string]any `json:"params"`
	}
	_ = json.Unmarshal([]byte(args), &call)

	return &agentModel.HumanReply{
		Service: rep.Service, EndpointId: rep.EndpointId, Title: rep.Title, Params: call.Params,
		StatusCode: rep.StatusCode, RequestId: rep.RequestId, Data: rep.Data,
		Rows: rep.Rows, TotalRows: rep.TotalRows, Truncated: rep.Truncated, MaskedFields: rep.Masked,
	}, false
}

// humanSent — что видит модель вместо ответа ручки для человека.
func humanSent(reply *agentModel.HumanReply) string {
	return fmt.Sprintf(localConstant.HumanReplySent, reply.StatusCode)
}

// collectHumanReplies — ответы ручек для человека из вызовов шага; сверх лимита — не отправлены
// (модели — ошибка вызова).
func collectHumanReplies(replies []agentModel.HumanReply, traces []agentModel.ToolTrace) []agentModel.HumanReply {
	for i := range traces {
		if traces[i].Human == nil {
			continue
		}
		if len(replies) >= localConstant.MaxHumanReplies {
			traces[i].Human = nil
			traces[i].Status = agentModel.ToolStatusToolError
			traces[i].Output = localConstant.ToolErrorPrefix + fmt.Sprintf(localConstant.HumanReplyLimit, localConstant.MaxHumanReplies)
			continue
		}
		replies = append(replies, *traces[i].Human)
	}
	return replies
}
