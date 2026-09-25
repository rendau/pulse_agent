package config

import (
	"time"

	"github.com/caarlos0/env/v9"
	_ "github.com/joho/godotenv/autoload"
)

// Conf — параметры окружения: подключения, порты, токены, лимиты.
var Conf = struct {
	Debug    bool   `env:"DEBUG" envDefault:"false"`
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`

	SystemHttpPort string `env:"SYSTEM_HTTP_PORT" envDefault:"3003"` // healthcheck, metrics, docs

	// API для систем-клиентов: POST /v1/ask, /v1/reset
	HttpPort string `env:"HTTP_PORT" envDefault:"80"`
	// ключи систем-клиентов: «имя:ключ» через запятую (pulse_bot:…,service-desk:…);
	// имя — в журнале и метриках, у каждой системы свои беседы
	ApiKeys []string `env:"API_KEYS" envSeparator:","`
	// ключ разработчика: /debug/* (мониторинг, прогон эталонов) и /v1/* как система debug;
	// пусто — /debug закрыт
	DebugToken string `env:"DEBUG_TOKEN"`
	// системы, которым можно запускать прогон эталонов (/v1/eval; бот — команда /eval для админов)
	EvalClients []string `env:"EVAL_CLIENTS" envSeparator:","`
	// прогон: вопросов одновременно и потолок времени на весь прогон
	EvalParallel int           `env:"EVAL_PARALLEL" envDefault:"3"`
	EvalTimeout  time.Duration `env:"EVAL_TIMEOUT" envDefault:"20m"`
	// журнал вопросов (мониторинг /debug/recent, /debug/stats): последние N в памяти
	JournalSize int `env:"JOURNAL_SIZE" envDefault:"500"`

	// LLM: провайдер выбирает адаптер (internal/service/llm/<provider>)
	LlmProvider        string `env:"LLM_PROVIDER" envDefault:"openai"`
	LlmModel           string `env:"LLM_MODEL" envDefault:"gpt-6-sol"`
	LlmReasoningEffort string `env:"LLM_REASONING_EFFORT" envDefault:"medium"`
	LlmMaxOutputTokens int64  `env:"LLM_MAX_OUTPUT_TOKENS" envDefault:"32000"` // на один шаг, вместе с reasoning

	OpenaiApiKey  string `env:"OPENAI_API_KEY"`
	OpenaiBaseUrl string `env:"OPENAI_BASE_URL"` // пусто — api.openai.com

	// pulse (MCP streamable HTTP); токен — bearer
	PulseMcpUrl   string `env:"PULSE_MCP_URL,required"`
	PulseMcpToken string `env:"PULSE_MCP_TOKEN"`

	// ограничители разбора
	AgentMaxToolCalls int           `env:"AGENT_MAX_TOOL_CALLS" envDefault:"20"`
	AgentTimeout      time.Duration `env:"AGENT_TIMEOUT" envDefault:"5m"`

	// история диалога (в памяти): последние N пар вопрос-ответ, сброс после тишины
	HistoryMaxTurns int           `env:"HISTORY_MAX_TURNS" envDefault:"10"`
	HistoryTtl      time.Duration `env:"HISTORY_TTL" envDefault:"1h"`

	// графики к ответам: dark | light (тему пользователя клиенты не сообщают — одна на сервис)
	ChartTheme string `env:"CHART_THEME" envDefault:"dark"`
}{}

func init() {
	if err := env.Parse(&Conf); err != nil {
		panic(err)
	}
}
