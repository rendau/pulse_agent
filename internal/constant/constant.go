package constant

const (
	ServiceName = "pulse_agent"
)

// Timezone — пояс времени в ответах и на графиках.
const Timezone = "Asia/Almaty"

// Version, Commit, BuiltAt подставляются при сборке: -ldflags "-X .../internal/constant.Version=<ver>"
// (Commit — полный SHA: манифест сервиса, build.commit).
var (
	Version = "dev"
	Commit  = ""
	BuiltAt = ""
)

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
