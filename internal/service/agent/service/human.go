package service

import (
	"encoding/json"
	"fmt"

	agentModel "github.com/rendau/pulse_agent/internal/service/agent/model"
	localConstant "github.com/rendau/pulse_agent/internal/service/agent/service/constant"
)

// humanReply — ответ call_service_endpoint от ручки для человека (audience: human): данные как
// есть, параметры — настоящими значениями (args — аргументы после подстановки токенов). Не та
// ручка или не разобрался — nil: ответ идёт модели обычным путём (с токенами вместо данных).
func humanReply(text, args string) *agentModel.HumanReply {
	var rep struct {
		Service    string          `json:"service"`
		EndpointId string          `json:"endpoint_id"`
		Title      string          `json:"title"`
		Audience   string          `json:"audience"`
		StatusCode int             `json:"status_code"`
		Data       json.RawMessage `json:"data"`
		Rows       int             `json:"rows"`
		TotalRows  int             `json:"total_rows"`
		Truncated  bool            `json:"truncated"`
		RequestId  string          `json:"request_id"`
		Masked     int             `json:"masked_fields"`
	}
	if json.Unmarshal([]byte(text), &rep) != nil || rep.Audience != localConstant.AudienceHuman {
		return nil
	}
	var call struct {
		Params map[string]any `json:"params"`
	}
	_ = json.Unmarshal([]byte(args), &call)

	return &agentModel.HumanReply{
		Service: rep.Service, EndpointId: rep.EndpointId, Title: rep.Title, Params: call.Params,
		StatusCode: rep.StatusCode, RequestId: rep.RequestId, Data: rep.Data,
		Rows: rep.Rows, TotalRows: rep.TotalRows, Truncated: rep.Truncated, MaskedFields: rep.Masked,
	}
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
