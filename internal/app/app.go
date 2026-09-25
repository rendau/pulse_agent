package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/lo"

	"github.com/mechta-market/pulse_agent/evals"
	"github.com/mechta-market/pulse_agent/internal/config"
	"github.com/mechta-market/pulse_agent/internal/constant"
	domainDialogRepoMemP "github.com/mechta-market/pulse_agent/internal/domain/dialog/repo/mem"
	domainDialogServiceP "github.com/mechta-market/pulse_agent/internal/domain/dialog/service"
	domainJournalRepoDbP "github.com/mechta-market/pulse_agent/internal/domain/journal/repo/db"
	domainJournalRepoMemP "github.com/mechta-market/pulse_agent/internal/domain/journal/repo/mem"
	domainJournalServiceP "github.com/mechta-market/pulse_agent/internal/domain/journal/service"
	"github.com/mechta-market/pulse_agent/internal/eval"
	handlerHttpP "github.com/mechta-market/pulse_agent/internal/handler/http"
	"github.com/mechta-market/pulse_agent/internal/infra/httpx"
	"github.com/mechta-market/pulse_agent/internal/infra/pulsekit"
	serviceAgentServiceP "github.com/mechta-market/pulse_agent/internal/service/agent/service"
	serviceChartServiceP "github.com/mechta-market/pulse_agent/internal/service/chart/service"
	"github.com/mechta-market/pulse_agent/internal/service/llm"
	serviceLlmOpenaiServiceP "github.com/mechta-market/pulse_agent/internal/service/llm/openai/service"
	servicePulseServiceP "github.com/mechta-market/pulse_agent/internal/service/pulse/service"
	"github.com/mechta-market/pulse_agent/internal/service/retention"
	serviceRetentionServiceP "github.com/mechta-market/pulse_agent/internal/service/retention/service"
	usecaseAskP "github.com/mechta-market/pulse_agent/internal/usecase/ask"
	usecaseMonitorP "github.com/mechta-market/pulse_agent/internal/usecase/monitor"
	monitorModel "github.com/mechta-market/pulse_agent/internal/usecase/monitor/model"
)

type App struct {
	pgpool *pgxpool.Pool // nil — журнал в памяти (PG_DSN пуст)
	pulse  *servicePulseServiceP.Service

	journalRetention retention.Retention // nil — журнал в памяти

	// pulsekit — манифест агента для pulse и фоновые проверки зависимостей (ручка состояния)
	pulsekit *pulsekit.Kit

	httpServer       *http.Server
	systemHttpServer *http.Server

	ctx       context.Context
	ctxCancel context.CancelFunc

	exitCode int
}

func (a *App) Init() {
	a.ctx, a.ctxCancel = context.WithCancel(context.Background())

	// logger
	initLogger(config.Conf.Debug, config.Conf.LogLevel)
	slog.Info("starting " + constant.ServiceName + " " + constant.Version)

	location, err := time.LoadLocation(constant.Timezone)
	errCheck(err, "timezone")

	// pgpool (журнал вопросов)
	if config.Conf.PgDsn != "" {
		errCheck(ensureDatabase(config.Conf.PgDsn), "ensure database")
		runMigrations(config.Conf.PgDsn)
		slog.Info("PG-migrations have been successfully applied")

		a.pgpool, err = initPgPool(config.Conf.PgDsn)
		errCheck(err, "pgpool init")
	} else {
		slog.Warn("PG_DSN is empty: journal is kept in memory (last JOURNAL_SIZE questions)")
	}

	// llm
	var llmProvider llm.Provider
	switch config.Conf.LlmProvider {
	case constant.LlmProviderOpenai:
		llmProvider = serviceLlmOpenaiServiceP.New(
			serviceLlmOpenaiServiceP.Config{
				ApiKey:          config.Conf.OpenaiApiKey,
				BaseUrl:         config.Conf.OpenaiBaseUrl,
				Model:           config.Conf.LlmModel,
				ReasoningEffort: config.Conf.LlmReasoningEffort,
				MaxOutputTokens: config.Conf.LlmMaxOutputTokens,
			},
			// ответ без стриминга приходит целиком после генерации (с reasoning —
			// минуты): заголовков ждём до общего таймаута разбора, его держит контекст
			httpx.New(httpx.Config{ResponseHeaderTimeout: config.Conf.AgentTimeout, VerifyTLS: true}),
		)
	default:
		errCheck(fmt.Errorf("unknown LLM_PROVIDER %q", config.Conf.LlmProvider), "llm")
	}
	slog.Info("llm", "provider", llmProvider.Name(), "model", config.Conf.LlmModel, "reasoning_effort", config.Conf.LlmReasoningEffort)

	// pulse (MCP)
	a.pulse = servicePulseServiceP.New(
		config.Conf.PulseMcpUrl,
		config.Conf.PulseMcpToken,
		// таймаут вызова инструмента держит контекст разбора
		httpx.New(httpx.Config{ResponseHeaderTimeout: 2 * time.Minute}),
	)

	// chart
	chartService := serviceChartServiceP.New(serviceChartServiceP.Config{Location: location, Theme: config.Conf.ChartTheme})

	// agent
	agentService := serviceAgentServiceP.New(
		serviceAgentServiceP.Config{
			MaxToolCalls: config.Conf.AgentMaxToolCalls,
			Timeout:      config.Conf.AgentTimeout,
		},
		llmProvider, a.pulse, chartService,
	)

	// dialog
	dialogRepo := domainDialogRepoMemP.New()
	dialogService := domainDialogServiceP.New(
		domainDialogServiceP.Config{MaxTurns: config.Conf.HistoryMaxTurns, Ttl: config.Conf.HistoryTtl},
		dialogRepo,
	)

	// journal
	var journalService *domainJournalServiceP.Service
	journalStorage, journalKeep := "memory", time.Duration(0)
	if a.pgpool != nil {
		journalStorage, journalKeep = "postgres", time.Duration(config.Conf.JournalRetentionDays)*24*time.Hour
		journalService = domainJournalServiceP.New(domainJournalRepoDbP.New(a.pgpool))
		a.journalRetention = serviceRetentionServiceP.New(
			serviceRetentionServiceP.Config{Keep: journalKeep, Interval: time.Hour},
			journalService,
		)
	} else {
		journalService = domainJournalServiceP.New(domainJournalRepoMemP.New(config.Conf.JournalSize))
	}

	// ask
	askUsecase := usecaseAskP.New(dialogService, journalService, agentService)

	// eval (эталонные вопросы, вшитые в образ)
	evalKeeper, err := eval.NewKeeper(evals.Cases, evals.Baseline, config.Conf.EvalParallel)
	errCheck(err, "evals")

	keys, err := parseApiKeys(config.Conf.ApiKeys)
	errCheck(err, "API_KEYS")
	if len(keys) == 0 {
		slog.Warn("API_KEYS is empty: only DEBUG_TOKEN is accepted")
	}

	// monitor
	monitorUsecase := usecaseMonitorP.New(monitorModel.Info{
		Version:          constant.Version,
		StartedAt:        time.Now(),
		LlmProvider:      llmProvider.Name(),
		LlmModel:         config.Conf.LlmModel,
		ReasoningEffort:  config.Conf.LlmReasoningEffort,
		MaxToolCalls:     config.Conf.AgentMaxToolCalls,
		Timeout:          config.Conf.AgentTimeout,
		Clients:          lo.Keys(keys),
		EvalClients:      config.Conf.EvalClients,
		EvalCases:        evalKeeper.Cases(),
		Journal:          journalStorage,
		JournalRetention: journalKeep,
	}, journalService, a.pulse)

	// http server (API)
	{
		handler := handlerHttpP.New(handlerHttpP.Config{
			Keys:        keys,
			DebugToken:  config.Conf.DebugToken,
			EvalClients: config.Conf.EvalClients,
			EvalTimeout: config.Conf.EvalTimeout,
		}, askUsecase, monitorUsecase, evalKeeper, location)
		a.httpServer = HttpServerCreate(config.Conf.HttpPort, handler, a.ctx)
	}

	// pulsekit (манифест агента: сведения о себе, зависимости, ручка question_stats)
	a.pulsekit = newPulsekit()
	{
		a.pulsekit.Depend("pulse", "http", hostOf(config.Conf.PulseMcpUrl), true, func(ctx context.Context) error {
			_, err := a.pulse.Catalog(ctx)
			return err
		})
		a.pulsekit.Depend("llm", "http", llmProvider.Name(), true, llmProvider.Ping)
		if a.pgpool != nil {
			a.pulsekit.Depend("journal_pg", "postgres", hostOf(config.Conf.PgDsn), false, a.pgpool.Ping)
		}
		handleQuestionStats(a.pulsekit, monitorUsecase)
	}

	// system http server (healthcheck, docs, metrics, манифест)
	{
		a.systemHttpServer = SystemHttpServerCreate(config.Conf.SystemHttpPort, a.pulsekit.Register)
	}
}

func (a *App) PreStartHook() {
	slog.Info("PreStartHook")
}

func (a *App) Start() {
	slog.Info("Starting")

	// pulsekit
	a.pulsekit.Start(a.ctx)

	// journal retention
	if a.journalRetention != nil {
		a.journalRetention.Start(a.ctx)
	}

	// http server
	{
		go func() {
			err := a.httpServer.ListenAndServe()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCheck(err, "http-server stopped")
			}
		}()
		slog.Info("http-server started " + a.httpServer.Addr)
	}

	// system http server
	{
		go func() {
			err := a.systemHttpServer.ListenAndServe()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCheck(err, "system-http-server stopped")
			}
		}()
		slog.Info("system-http-server started " + a.systemHttpServer.Addr)
	}
}

func (a *App) Listen() {
	signalCtx, signalCtxCancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer signalCtxCancel()

	// wait signal
	<-signalCtx.Done()
}

func (a *App) Stop() {
	slog.Info("Shutting down...")

	// stop context: отменяет идущие разборы (клиенты получают 503 canceled)
	a.ctxCancel()

	// http server
	{
		ctx, ctxCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer ctxCancel()

		if err := a.httpServer.Shutdown(ctx); err != nil {
			slog.Error("http-server shutdown error", "error", err)
			a.exitCode = 1
		}
	}

	// system http server
	{
		ctx, ctxCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer ctxCancel()

		if err := a.systemHttpServer.Shutdown(ctx); err != nil {
			slog.Error("system-http-server shutdown error", "error", err)
			a.exitCode = 1
		}
	}
}

func (a *App) WaitJobs() {
	slog.Info("waiting jobs")

	// pulsekit
	a.pulsekit.Wait()

	// journal retention
	if a.journalRetention != nil {
		a.journalRetention.Wait()
	}
}

func (a *App) Exit() {
	slog.Info("Exit")

	if err := a.pulse.Close(); err != nil {
		slog.Warn("pulse session close", "error", err)
	}

	if a.pgpool != nil {
		a.pgpool.Close()
	}

	os.Exit(a.exitCode)
}

// parseApiKeys — «имя:ключ» → имя → ключ.
func parseApiKeys(entries []string) (map[string]string, error) {
	keys := make(map[string]string, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, key, ok := strings.Cut(entry, ":")
		name, key = strings.TrimSpace(name), strings.TrimSpace(key)
		if !ok || name == "" || key == "" {
			return nil, fmt.Errorf("entry %q: expected name:key", name)
		}
		if _, dup := keys[name]; dup {
			return nil, fmt.Errorf("duplicate client %q", name)
		}
		keys[name] = key
	}
	return keys, nil
}

func errCheck(err error, msg string) {
	if err != nil {
		if msg != "" {
			err = fmt.Errorf("%s: %w", msg, err)
		}
		slog.Error(err.Error())
		os.Exit(1)
	}
}
