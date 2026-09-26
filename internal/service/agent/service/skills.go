package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/samber/lo"
	"go.yaml.in/yaml/v3"

	agentModel "github.com/rendau/pulse_agent/internal/service/agent/model"
	localConstant "github.com/rendau/pulse_agent/internal/service/agent/service/constant"
	llmModel "github.com/rendau/pulse_agent/internal/service/llm/model"
)

// ParseSkills — навыки из *.md: front matter (name, title, when, requires: chat) и текст
// руководства. Порядок — по имени файла.
func ParseSkills(files fs.FS) ([]agentModel.Skill, error) {
	names, err := fs.Glob(files, "*.md")
	if err != nil {
		return nil, fmt.Errorf("glob: %w", err)
	}
	slices.Sort(names)

	skills := make([]agentModel.Skill, 0, len(names))
	for _, name := range names {
		raw, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		head, body, ok := bytes.Cut(bytes.TrimPrefix(raw, []byte("---\n")), []byte("\n---\n"))
		if !ok || !bytes.HasPrefix(raw, []byte("---\n")) {
			return nil, fmt.Errorf("%s: no front matter", name)
		}
		var meta struct {
			Name     string `yaml:"name"`
			Title    string `yaml:"title"`
			When     string `yaml:"when"`
			Requires string `yaml:"requires"`
		}
		if err = yaml.Unmarshal(head, &meta); err != nil {
			return nil, fmt.Errorf("%s: front matter: %w", name, err)
		}
		if meta.Name != strings.TrimSuffix(path.Base(name), ".md") || meta.Title == "" || meta.When == "" {
			return nil, fmt.Errorf("%s: name must match the file, title and when are required", name)
		}
		skills = append(skills, agentModel.Skill{
			Name: meta.Name, Title: meta.Title, When: meta.When, Body: strings.TrimSpace(string(body)),
			RequiresChat: meta.Requires == "chat",
		})
	}
	return skills, nil
}

// availableSkills — навыки для разбора: требующие беседы — только в беседе.
func availableSkills(skills []agentModel.Skill, chat bool) []agentModel.Skill {
	return lo.Filter(skills, func(s agentModel.Skill, _ int) bool { return chat || !s.RequiresChat })
}

// skillsList — строки навыков для системного промпта.
func skillsList(skills []agentModel.Skill) string {
	return strings.Join(lo.Map(skills, func(s agentModel.Skill, _ int) string {
		return fmt.Sprintf("- %s — %s: %s", s.Name, s.Title, s.When)
	}), "\n")
}

// skillTool — open_skill с перечнем доступных навыков.
func skillTool(skills []agentModel.Skill) llmModel.ToolDef {
	return llmModel.ToolDef{
		Name:        localConstant.SkillTool,
		Description: localConstant.SkillDescription,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"enum":        lo.Map(skills, func(s agentModel.Skill, _ int) any { return s.Name }),
					"description": "имя навыка из списка в инструкциях",
				},
			},
			"required": []any{"name"},
		},
	}
}

// callSkill — текст навыка; неизвестный — ошибка модели со списком.
func (s *Service) callSkill(step int, call llmModel.ToolCall, skills []agentModel.Skill) agentModel.ToolTrace {
	trace := agentModel.ToolTrace{Step: step, Name: call.Name, Arguments: call.Arguments}
	var args struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal([]byte(call.Arguments), &args)
	skill, ok := lo.Find(skills, func(sk agentModel.Skill) bool { return sk.Name == args.Name })
	if !ok {
		trace.Status = agentModel.ToolStatusToolError
		trace.Output = localConstant.ToolErrorPrefix + fmt.Sprintf("unknown skill %q; available: %s", args.Name,
			strings.Join(lo.Map(skills, func(sk agentModel.Skill, _ int) string { return sk.Name }), ", "))
	} else {
		trace.Status, trace.Output = agentModel.ToolStatusOk, "# "+skill.Title+"\n\n"+skill.Body
	}
	metricToolCalls.WithLabelValues(call.Name, trace.Status).Inc()
	return trace
}
