package service

import (
	"encoding/json"
	"fmt"
	"time"

	notifyModel "github.com/mechta-market/pulse_agent/internal/domain/notify/model"
)

// типы событий get_timeline, из которых наблюдатель делает сигналы
const (
	eventDeploy = "deploy"
	eventAlert  = "alert_firing"
)

// timelineRep — нужная наблюдателю часть ответа get_timeline.
type timelineRep struct {
	Events []timelineEvent `json:"events"`
}

type timelineEvent struct {
	Ts      time.Time      `json:"ts"`
	Type    string         `json:"type"`
	Service string         `json:"service"`
	Summary string         `json:"summary"`
	Details map[string]any `json:"details"`
}

// snapshotRep — нужная наблюдателю часть ответа get_service_snapshot.
type snapshotRep struct {
	Health       string   `json:"health"`
	SummaryHints []string `json:"summary_hints"`
}

// signals — сигналы из событий ленты: выкатка — разбор через deployDelay, алерт — сразу.
// Алерт без имени и алерты severity none пропускаются.
func signals(events []timelineEvent, now time.Time, deployDelay time.Duration) []*notifyModel.Signal {
	out := make([]*notifyModel.Signal, 0)
	for _, e := range events {
		details, _ := json.Marshal(e.Details)
		switch e.Type {
		case eventDeploy:
			ref := firstString(e.Details, "commit", "image_digest")
			if ref == "" {
				continue
			}
			out = append(out, &notifyModel.Signal{
				Key:  fmt.Sprintf("deploy:%s:%s", firstString(e.Details, "workload"), ref),
				Kind: notifyModel.KindDeploy, Service: e.Service, At: e.Ts, DueAt: e.Ts.Add(deployDelay),
				Summary: e.Summary, Details: details,
			})
		case eventAlert:
			labels, _ := e.Details["labels"].(map[string]any)
			name := firstString(labels, "alertname")
			if name == "" || firstString(labels, "severity") == "none" {
				continue
			}
			at := e.Ts
			if last, err := time.Parse(time.RFC3339, firstString(e.Details, "last_at")); err == nil {
				at = last
			}
			out = append(out, &notifyModel.Signal{
				Key:  fmt.Sprintf("alert:%s:%s", e.Service, name),
				Kind: notifyModel.KindAlert, Service: e.Service, At: at, DueAt: now,
				Summary: e.Summary, Details: details,
			})
		}
	}
	return out
}

// firstString — первое непустое строковое значение по ключам.
func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// signalRef — чем сигнал отличается от других того же сервиса: имя алерта, коммит выкатки.
func signalRef(s *notifyModel.Signal) string {
	var details map[string]any
	_ = json.Unmarshal(s.Details, &details)
	switch s.Kind {
	case notifyModel.KindAlert:
		labels, _ := details["labels"].(map[string]any)
		return firstString(labels, "alertname")
	case notifyModel.KindDeploy:
		ref := firstString(details, "commit", "image_digest")
		return ref[:min(len(ref), 7)]
	}
	return ""
}
