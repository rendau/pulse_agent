package app

import (
	"context"
	"net/url"
	"time"

	"github.com/mechta-market/pulse_agent/internal/constant"
	"github.com/mechta-market/pulse_agent/internal/infra/pulsekit"
	usecaseMonitorP "github.com/mechta-market/pulse_agent/internal/usecase/monitor"
)

// манифест агента по стандарту pulse (pulse/docs/service-manifest.md)

func newPulsekit() *pulsekit.Kit {
	return pulsekit.New(pulsekit.Config{}, pulsekit.Service{
		Name:  "pulse_agent",
		Title: "pulse agent — разбор вопросов об инфраструктуре",
		Description: "Агент с JSON-API: отвечает на вопросы людей и систем (Telegram-бот, service-desk) об " +
			"инфраструктуре — LLM с инструментами pulse, графики, журнал вопросов.",
		Aliases:     []string{"pulse-agent", "агент pulse", "агент пульса"},
		OwnerTeam:   "platform",
		Criticality: "low",
		RepoUrl:     "https://github.com/mechta-market/pulse_agent",
	}, pulsekit.Build{Version: constant.Version, Commit: constant.Commit, BuiltAt: constant.BuiltAt})
}

// questionStatsRep — ответ ручки question_stats: только агрегаты, без текстов вопросов и имён.
type questionStatsRep struct {
	Since     time.Time        `json:"since"`
	Questions int              `json:"questions"`
	Clients   []clientStatsRep `json:"clients"`
	Tools     []toolStatsRep   `json:"tools"`
}

type clientStatsRep struct {
	Client        string         `json:"client"`
	Questions     int            `json:"questions"`
	Outcomes      map[string]int `json:"outcomes"`
	AvgDurationMs int64          `json:"avg_duration_ms"`
	P95DurationMs int64          `json:"p95_duration_ms"`
	// расход LLM (единицы провайдера): «token» в имени поля стандарт запрещает
	LlmInput  int64 `json:"llm_input" pulse:"description=входные единицы LLM"`
	LlmOutput int64 `json:"llm_output" pulse:"description=выходные единицы LLM"`
}

type toolStatsRep struct {
	Tool  string `json:"tool"`
	Calls int    `json:"calls"`
}

func handleQuestionStats(kit *pulsekit.Kit, monitor *usecaseMonitorP.Usecase) {
	pulsekit.Handle(kit, pulsekit.Endpoint{
		Id:    "question_stats",
		Title: "Сводка вопросов к агенту",
		Description: "Вызывай, когда спрашивают, как работает агент или бот: сколько вопросов за окно по системам, " +
			"чем закончились (answered, incomplete, error…), сколько длились, какие инструменты pulse звались чаще. " +
			"Без текстов вопросов и имён.",
		Path: "/diag/questions",
		Params: map[string]pulsekit.Param{
			"window": {Enum: []string{"1h", "24h", "7d"}, Default: "24h", Description: "окно сводки"},
		},
		Timeout: 3 * time.Second,
	}, func(ctx context.Context, params map[string]string) (questionStatsRep, error) {
		stats, err := monitor.Stats(ctx, params["window"])
		if err != nil {
			return questionStatsRep{}, err
		}
		rep := questionStatsRep{Since: stats.Since, Questions: stats.Questions}
		for _, c := range stats.Clients {
			rep.Clients = append(rep.Clients, clientStatsRep{
				Client: c.Client, Questions: c.Questions, Outcomes: c.Outcomes,
				AvgDurationMs: c.AvgDuration.Milliseconds(), P95DurationMs: c.P95Duration.Milliseconds(),
				LlmInput: c.InputTokens, LlmOutput: c.OutputTokens,
			})
		}
		for _, tool := range stats.Tools {
			rep.Tools = append(rep.Tools, toolStatsRep{Tool: tool.Tool, Calls: tool.Calls})
		}
		return rep, nil
	})
}

// hostOf — хост адреса для target зависимости: без схемы, пути и учётных данных.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}
