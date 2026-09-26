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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/lo"

	"github.com/mechta-market/pulse_agent/evals"
	"github.com/mechta-market/pulse_agent/internal/config"
	"github.com/mechta-market/pulse_agent/internal/constant"
	domainChatRepoDbP "github.com/mechta-market/pulse_agent/internal/domain/chat/repo/db"
	domainChatServiceP "github.com/mechta-market/pulse_agent/internal/domain/chat/service"
	domainDialogRepoMemP "github.com/mechta-market/pulse_agent/internal/domain/dialog/repo/mem"
	domainDialogServiceP "github.com/mechta-market/pulse_agent/internal/domain/dialog/service"
	domainJournalRepoDbP "github.com/mechta-market/pulse_agent/internal/domain/journal/repo/db"
	domainJournalRepoMemP "github.com/mechta-market/pulse_agent/internal/domain/journal/repo/mem"
	domainJournalServiceP "github.com/mechta-market/pulse_agent/internal/domain/journal/service"
	domainNotifyRepoDbP "github.com/mechta-market/pulse_agent/internal/domain/notify/repo/db"
	domainNotifyServiceP "github.com/mechta-market/pulse_agent/internal/domain/notify/service"
	"github.com/mechta-market/pulse_agent/internal/eval"
	handlerHttpP "github.com/mechta-market/pulse_agent/internal/handler/http"
	"github.com/mechta-market/pulse_agent/internal/infra/httpx"
	"github.com/mechta-market/pulse_agent/internal/infra/pulsekit"
	serviceAgentServiceP "github.com/mechta-market/pulse_agent/internal/service/agent/service"
	serviceChartServiceP "github.com/mechta-market/pulse_agent/internal/service/chart/service"
	serviceChattoolsServiceP "github.com/mechta-market/pulse_agent/internal/service/chattools/service"
	"github.com/mechta-market/pulse_agent/internal/service/llm"
	llmModel "github.com/mechta-market/pulse_agent/internal/service/llm/model"
	serviceLlmOpenaiServiceP "github.com/mechta-market/pulse_agent/internal/service/llm/openai/service"
	servicePiiServiceP "github.com/mechta-market/pulse_agent/internal/service/pii/service"
	servicePulseServiceP "github.com/mechta-market/pulse_agent/internal/service/pulse/service"
	"github.com/mechta-market/pulse_agent/internal/service/retention"
	serviceRetentionServiceP "github.com/mechta-market/pulse_agent/internal/service/retention/service"
	"github.com/mechta-market/pulse_agent/internal/service/watch"
	serviceWatchServiceP "github.com/mechta-market/pulse_agent/internal/service/watch/service"
	usecaseAskP "github.com/mechta-market/pulse_agent/internal/usecase/ask"
	usecaseMonitorP "github.com/mechta-market/pulse_agent/internal/usecase/monitor"
	monitorModel "github.com/mechta-market/pulse_agent/internal/usecase/monitor/model"
	usecaseNotifyP "github.com/mechta-market/pulse_agent/internal/usecase/notify"
	"github.com/mechta-market/pulse_agent/skills"
)

type App struct {
	pgpool *pgxpool.Pool // nil — журнал в памяти (PG_DSN пуст)
	pulse  *servicePulseServiceP.Service

	journalRetention retention.Retention // nil — журнал в памяти
	watch            watch.Watch         // nil — наблюдатель выключен или нет хранилища

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
	// платный API: здоровье — по настоящим вызовам (кончились деньги — Ping этого не видит)
	llmCalls := pulsekit.NewPassive(15 * time.Minute)
	var llmNoCredits atomic.Bool // последний вызов: у провайдера кончились деньги или квота
	llmProvider = llm.Observed(llmProvider, func(err error) {
		llmNoCredits.Store(errors.Is(err, llmModel.ErrNoCredits))
		llmCalls.Observe(err)
	})

	// pulse (MCP)
	a.pulse = servicePulseServiceP.New(
		config.Conf.PulseMcpUrl,
		config.Conf.PulseMcpToken,
		// таймаут вызова инструмента держит контекст разбора
		httpx.New(httpx.Config{ResponseHeaderTimeout: 2 * time.Minute}),
	)

	// chart
	chartService := serviceChartServiceP.New(serviceChartServiceP.Config{Location: location, Theme: config.Conf.ChartTheme})

	// pii (персональные данные токенами на границе с моделью)
	piiService := servicePiiServiceP.New(servicePiiServiceP.Config{
		Key:         []byte(config.Conf.PiiTokenKey),
		CountryCode: config.Conf.PiiPhoneCountryCode,
	})

	// chat, notify (контекст бесед, уведомления наблюдателя — только с хранилищем)
	// (nil-интерфейсы, а не nil-указатели: без хранилища компоненты работают без них)
	var chatService *domainChatServiceP.Service
	var notifyService *domainNotifyServiceP.Service
	var askChat usecaseAskP.ChatServiceI
	var agentChatTools serviceAgentServiceP.ChatToolsI
	if a.pgpool != nil {
		chatService = domainChatServiceP.New(domainChatRepoDbP.New(a.pgpool))
		notifyService = domainNotifyServiceP.New(domainNotifyRepoDbP.New(a.pgpool))
		askChat = chatService
		agentChatTools = serviceChattoolsServiceP.New(chatService, notifyService)
	}

	// skills (навыки агента, вшиты в образ)
	agentSkills, err := serviceAgentServiceP.ParseSkills(skills.Files)
	errCheck(err, "skills")

	// agent
	agentService := serviceAgentServiceP.New(
		serviceAgentServiceP.Config{
			MaxToolCalls: config.Conf.AgentMaxToolCalls,
			Timeout:      config.Conf.AgentTimeout,
		},
		llmProvider, a.pulse, chartService, piiService, agentChatTools, agentSkills,
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
	askUsecase := usecaseAskP.New(askChat, dialogService, journalService, agentService, piiService)

	// notify (лента для бесед клиентов) и наблюдатель
	var notifyUsecase handlerHttpP.NotifyUsecaseI
	if notifyService != nil {
		notifyUsecase = usecaseNotifyP.New(chatService, notifyService)
		if config.Conf.WatchEnabled {
			a.watch = serviceWatchServiceP.New(serviceWatchServiceP.Config{
				Interval:       config.Conf.WatchInterval,
				DeployDelay:    config.Conf.WatchDeployDelay,
				AlertRepeat:    config.Conf.WatchAlertRepeat,
				MaxRunsPerHour: config.Conf.WatchMaxRunsPerHour,
				Keep:           journalKeep,
			}, a.pulse, agentService, notifyService, journalService)
		}
	} else {
		slog.Warn("PG_DSN is empty: no notifications, watcher and chat context")
	}

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
		}, askUsecase, notifyUsecase, monitorUsecase, evalKeeper, location)
		a.httpServer = HttpServerCreate(config.Conf.HttpPort, handler, a.ctx)
	}

	// pulsekit (манифест агента: сведения о себе, зависимости, ручка question_stats)
	a.pulsekit = newPulsekit()
	{
		a.pulsekit.Depend("pulse", "http", pulsekit.Host(config.Conf.PulseMcpUrl), true, func(ctx context.Context) error {
			_, err := a.pulse.Catalog(ctx)
			return err
		}).Affects("ответы на вопросы и разборы уведомлений")
		a.pulsekit.Depend("llm", "http", llmProvider.Name(), true, func(ctx context.Context) error {
			if !llmCalls.Idle() {
				err := llmCalls.Check(ctx)
				if err != nil && llmNoCredits.Load() {
					return pulsekit.Problem{Status: "down", Message: "у провайдера LLM кончились деньги или квота — нужно пополнить"}
				}
				return err
			}
			return llmProvider.Ping(ctx)
		}).Affects("ответы на вопросы и разборы уведомлений")
		if a.pgpool != nil {
			a.pulsekit.Depend("journal_pg", "postgres", pulsekit.Host(config.Conf.PgDsn), false, a.pgpool.Ping).Affects("журнал вопросов")
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

	// watch
	if a.watch != nil {
		a.watch.Start(a.ctx)
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

	// watch
	if a.watch != nil {
		a.watch.Wait()
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
