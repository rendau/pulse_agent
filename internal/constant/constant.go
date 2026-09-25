package constant

const (
	ServiceName = "pulse_agent"
)

// Timezone — пояс времени в ответах и на графиках.
const Timezone = "Asia/Almaty"

// Version подставляется при сборке: -ldflags "-X .../internal/constant.Version=<ver>".
var Version = "dev"

// LLM-провайдеры (LLM_PROVIDER)
const (
	LlmProviderOpenai = "openai"
)

// исход вопроса (метрики)
const (
	OutcomeAnswered   = "answered"
	OutcomeIncomplete = "incomplete"
	OutcomeBusy       = "busy"
	OutcomeError      = "error"
)
