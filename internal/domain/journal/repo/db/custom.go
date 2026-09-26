package db

import (
	"github.com/rendau/pulse_agent/internal/domain/journal/model"
)

func (r *Repo) getConditions(f model.Filter) (map[string]any, map[string][]any) {
	conditions := make(map[string]any, 2)
	conditionExps := make(map[string][]any, 2)

	if f.Client != "" {
		conditions["client"] = f.Client
	}
	if f.Outcome != "" {
		conditions["outcome"] = f.Outcome
	}
	if !f.Since.IsZero() {
		conditionExps["at >= ?"] = []any{f.Since}
	}
	if f.BeforeId > 0 {
		conditionExps["id < ?"] = []any{f.BeforeId}
	}

	return conditions, conditionExps
}
