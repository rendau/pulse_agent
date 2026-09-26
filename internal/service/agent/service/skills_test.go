package service

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentModel "github.com/rendau/pulse_agent/internal/service/agent/model"
	llmModel "github.com/rendau/pulse_agent/internal/service/llm/model"
	"github.com/rendau/pulse_agent/skills"
)

// Навыки из образа разбираются: имя совпадает с файлом, есть название и «когда».
func TestParseSkillsEmbedded(t *testing.T) {
	parsed, err := ParseSkills(skills.Files)
	require.NoError(t, err)
	names := lo.Map(parsed, func(s agentModel.Skill, _ int) string { return s.Name })
	assert.Contains(t, names, "service")
	assert.Contains(t, names, "notifications")
	for _, s := range parsed {
		assert.NotEmpty(t, s.Body, s.Name)
	}

	notifications, _ := lo.Find(parsed, func(s agentModel.Skill) bool { return s.Name == "notifications" })
	assert.True(t, notifications.RequiresChat)
	assert.NotContains(t, lo.Map(availableSkills(parsed, false), func(s agentModel.Skill, _ int) string { return s.Name }), "notifications",
		"без беседы — без навыка уведомлений")
}

func TestParseSkillsInvalid(t *testing.T) {
	_, err := ParseSkills(fstest.MapFS{"a.md": {Data: []byte("# без front matter")}})
	require.Error(t, err)
	_, err = ParseSkills(fstest.MapFS{"a.md": {Data: []byte("---\nname: b\ntitle: T\nwhen: W\n---\nтекст")}})
	require.ErrorContains(t, err, "name must match")
}

func TestOpenSkill(t *testing.T) {
	testSkills := []agentModel.Skill{
		{Name: "search", Title: "Поиск по номеру", When: "номер заказа", Body: "query_logs без service"},
		{Name: "notifications", Title: "Уведомления", When: "подписки", Body: "…", RequiresChat: true},
	}
	llm := &fakeLlm{steps: []*llmModel.Response{
		toolStep("s1",
			llmModel.ToolCall{Id: "c1", Name: "open_skill", Arguments: `{"name":"search"}`},
			llmModel.ToolCall{Id: "c2", Name: "resolve_service", Arguments: `{"query":"caravan"}`}),
		textStep("ок"),
	}}
	pulse := &fakePulse{}
	res, err := New(Config{MaxToolCalls: 20, Timeout: 5 * time.Minute}, llm, pulse, nil, testPii, nil, testSkills).
		Run(context.Background(), &agentModel.Req{Question: "что по заказу 234115?"})
	require.NoError(t, err)

	first := llm.requests[0]
	assert.Contains(t, first.System, "- search — Поиск по номеру: номер заказа")
	assert.NotContains(t, first.System, "notifications — ", "без беседы навык уведомлений не показан")
	skillDef, ok := lo.Find(first.Tools, func(d llmModel.ToolDef) bool { return d.Name == "open_skill" })
	require.True(t, ok)
	assert.Equal(t, []any{"search"}, skillDef.Parameters["properties"].(map[string]any)["name"].(map[string]any)["enum"])

	assert.Equal(t, "# Поиск по номеру\n\nquery_logs без service", res.Trace[0].Output)
	assert.Len(t, pulse.calls, 1, "навык — не вызов pulse")
}
