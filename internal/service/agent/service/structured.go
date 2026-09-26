package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/samber/lo"

	agentModel "github.com/rendau/pulse_agent/internal/service/agent/model"
)

// structuredRep — JSON ответа модели по constant.ResultSchema.
type structuredRep struct {
	Summary  string `json:"summary"`
	Status   string `json:"status"`
	Severity string `json:"severity"`
	Services []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Note   string `json:"note"`
	} `json:"services"`
	Facts []struct {
		Text    string  `json:"text"`
		Time    *string `json:"time"`
		Service *string `json:"service"`
	} `json:"facts"`
	NextSteps          []string `json:"next_steps"`
	UnavailableSources []string `json:"unavailable_sources"`
}

// decodeStructured разбирает ответ модели в формате json. Время факта, которое не удалось
// разобрать, дописывается в текст факта, а не теряется.
func decodeStructured(text string) (*agentModel.Structured, error) {
	var rep structuredRep
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &rep); err != nil {
		return nil, fmt.Errorf("decode structured answer: %w", err)
	}
	if strings.TrimSpace(rep.Summary) == "" {
		return nil, fmt.Errorf("decode structured answer: empty summary")
	}

	result := &agentModel.Structured{
		Summary: rep.Summary, Status: rep.Status, Severity: rep.Severity,
		NextSteps: rep.NextSteps, UnavailableSources: rep.UnavailableSources,
	}
	for _, s := range rep.Services {
		result.Services = append(result.Services, agentModel.StructuredService{Name: s.Name, Status: s.Status, Note: s.Note})
	}
	for _, f := range rep.Facts {
		fact := agentModel.Fact{Text: f.Text, Service: lo.FromPtr(f.Service)}
		if f.Time != nil && *f.Time != "" {
			if ts, err := time.Parse(time.RFC3339, *f.Time); err == nil {
				fact.Time = &ts
			} else {
				fact.Text = *f.Time + " — " + fact.Text
			}
		}
		result.Facts = append(result.Facts, fact)
	}
	return result, nil
}

// renderStructured — текст из полей: для истории беседы и поля answer у клиентов, которым
// нужен и текст. Порядок — как у текстового ответа: вывод, факты, что дальше.
func renderStructured(s *agentModel.Structured) string {
	var b strings.Builder
	b.WriteString(s.Summary)

	if len(s.Facts) > 0 {
		b.WriteString("\n")
		for _, f := range s.Facts {
			b.WriteString("\n- ")
			if f.Time != nil {
				b.WriteString(f.Time.Format("02.01 15:04 -07:00") + " — ")
			}
			if f.Service != "" {
				b.WriteString(f.Service + ": ")
			}
			b.WriteString(f.Text)
		}
	}
	if len(s.NextSteps) > 0 {
		b.WriteString("\n\nДальше:")
		for _, step := range s.NextSteps {
			b.WriteString("\n- " + step)
		}
	}
	if len(s.UnavailableSources) > 0 {
		b.WriteString("\n\nНе ответили источники: " + strings.Join(s.UnavailableSources, ", ") + ".")
	}
	return b.String()
}
