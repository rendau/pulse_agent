// Package ask — вопрос агенту от системы-клиента: формат, «один вопрос за раз на беседу»,
// история беседы, агент, метрики по клиентам. Доступ проверяет транспорт (ключ API).
package ask

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/samber/lo"

	"github.com/mechta-market/pulse_agent/internal/constant"
	dialogModel "github.com/mechta-market/pulse_agent/internal/domain/dialog/model"
	journalModel "github.com/mechta-market/pulse_agent/internal/domain/journal/model"
	"github.com/mechta-market/pulse_agent/internal/errs"
	"github.com/mechta-market/pulse_agent/internal/infra/metrics"
	agentModel "github.com/mechta-market/pulse_agent/internal/service/agent/model"
	agentConstant "github.com/mechta-market/pulse_agent/internal/service/agent/service/constant"
	"github.com/mechta-market/pulse_agent/internal/usecase/ask/model"
)

// maxQuestionChars — вопрос длиннее — ошибка клиента, а не повод жечь токены.
const maxQuestionChars = 4000

var (
	metricQuestions      *prometheus.CounterVec
	metricAnswerDuration *prometheus.HistogramVec
)

func init() {
	metricQuestions = metrics.Factory.NewCounterVec(prometheus.CounterOpts{
		Name: "question_total",
		Help: "Вопросы по клиенту и исходу: answered, incomplete, busy, error.",
	}, []string{"client", "outcome"})

	metricAnswerDuration = metrics.Factory.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "answer_duration_seconds",
		Help:    "Время от вопроса до ответа (разобранные вопросы).",
		Buckets: []float64{5, 10, 20, 40, 60, 120, 180, 300},
	}, []string{"client"})
}

type Usecase struct {
	dialog  DialogServiceI
	journal JournalServiceI
	agent   AgentI
	pii     PiiI

	mu   sync.Mutex
	busy map[string]struct{} // беседы, где идёт разбор
}

func New(dialog DialogServiceI, journal JournalServiceI, agent AgentI, pii PiiI) *Usecase {
	return &Usecase{dialog: dialog, journal: journal, agent: agent, pii: pii, busy: map[string]struct{}{}}
}

// Ask разбирает вопрос и пишет его в журнал (мониторинг). Ошибки: errs.InvalidRequest —
// пустой или слишком длинный вопрос, неизвестный формат; errs.Busy — в беседе уже идёт разбор.
func (u *Usecase) Ask(ctx context.Context, q *model.Question) (*model.Answer, error) {
	started := time.Now()
	answer, err := u.askChecked(ctx, q)
	u.record(ctx, q, answer, err, time.Since(started))
	return answer, err
}

func (u *Usecase) askChecked(ctx context.Context, q *model.Question) (*model.Answer, error) {
	text := strings.TrimSpace(q.Text)
	switch {
	case text == "":
		return nil, fmt.Errorf("%w: empty question", errs.InvalidRequest)
	case len([]rune(text)) > maxQuestionChars:
		return nil, fmt.Errorf("%w: question is longer than %d characters", errs.InvalidRequest, maxQuestionChars)
	}
	format := lo.CoalesceOrEmpty(q.Format, agentConstant.FormatTelegram)
	if q.ResponseSchema != nil {
		if q.Format != "" && q.Format != agentConstant.FormatJson {
			return nil, fmt.Errorf("%w: response_schema requires format json (or empty), got %q", errs.InvalidRequest, q.Format)
		}
		format = agentConstant.FormatJson
	}
	if !agentConstant.FormatKnown(format) {
		return nil, fmt.Errorf("%w: format %q; expected telegram, markdown, plain or json", errs.InvalidRequest, q.Format)
	}

	conversation := conversationKey(q)
	if conversation != "" {
		if !u.lock(conversation) {
			metricQuestions.WithLabelValues(q.Client, constant.OutcomeBusy).Inc()
			return nil, errs.Busy
		}
		defer u.unlock(conversation)
	}

	started := time.Now()

	answer, err := u.ask(ctx, conversation, q, text, format)
	if err != nil {
		metricQuestions.WithLabelValues(q.Client, constant.OutcomeError).Inc()
		return nil, err
	}

	metricAnswerDuration.WithLabelValues(q.Client).Observe(time.Since(started).Seconds())
	metricQuestions.WithLabelValues(q.Client, lo.Ternary(answer.Incomplete == "", constant.OutcomeAnswered, constant.OutcomeIncomplete)).Inc()

	return answer, nil
}

func (u *Usecase) ask(ctx context.Context, conversation string, q *model.Question, text, format string) (*model.Answer, error) {
	var history []*dialogModel.Turn
	if conversation != "" {
		var err error
		if history, err = u.dialog.History(ctx, conversation); err != nil {
			return nil, fmt.Errorf("dialog.History: %w", err)
		}
	}

	result, err := u.agent.Run(ctx, &agentModel.Req{
		History: lo.Map(history, func(t *dialogModel.Turn, _ int) agentModel.Turn {
			return agentModel.Turn{Question: t.Question, Answer: t.Answer}
		}),
		Question:       text,
		Format:         format,
		Charts:         q.Charts,
		ResponseSchema: q.ResponseSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("agent.Run: %w", err)
	}

	slog.Info("question answered",
		"client", q.Client,
		"conversation", q.ConversationId,
		"user", q.User.Id,
		"format", format,
		"client_schema", q.ResponseSchema != nil,
		"steps", result.Steps,
		"tool_calls", result.ToolCalls,
		"incomplete", result.Incomplete,
		"input_tokens", result.Usage.InputTokens,
		"cached_tokens", result.Usage.CachedTokens,
		"output_tokens", result.Usage.OutputTokens,
		"charts", len(result.Charts),
	)

	// пустой ответ в историю не кладём: он только собьёт следующий вопрос. В истории — то, что
	// видела модель (токены): иначе имя из ответа ушло бы модели со следующим вопросом
	if conversation != "" && result.ModelAnswer != "" {
		if err = u.dialog.Append(ctx, conversation, u.pii.Mask(text), result.ModelAnswer); err != nil {
			return nil, fmt.Errorf("dialog.Append: %w", err)
		}
	}

	return &model.Answer{
		Text:        result.Answer,
		ModelAnswer: result.ModelAnswer,
		Structured:  result.Structured,
		Json:        result.Json,
		Incomplete:  result.Incomplete,
		Charts:      result.Charts,
		Steps:       result.Steps,
		ToolCalls:   result.ToolCalls,
		Usage:       result.Usage,
		Trace:       result.Trace,
	}, nil
}

// record — запись вопроса в журнал; ошибка журнала не роняет ответ. Вопрос и ответ — как их
// видела модель: персональные данные токенами.
func (u *Usecase) record(ctx context.Context, q *model.Question, answer *model.Answer, err error, duration time.Duration) {
	entry := &journalModel.Entry{
		At: time.Now(), Client: q.Client, ConversationId: q.ConversationId, UserId: q.User.Id, UserName: q.User.Name,
		Question: u.pii.Mask(strings.TrimSpace(q.Text)), Format: q.Format, ClientSchema: q.ResponseSchema != nil,
		Duration: duration,
	}
	switch {
	case errors.Is(err, errs.Busy):
		entry.Outcome = journalModel.OutcomeBusy
	case errors.Is(err, errs.InvalidRequest):
		entry.Outcome, entry.Error = journalModel.OutcomeInvalid, err.Error()
	case err != nil:
		entry.Outcome, entry.Error = journalModel.OutcomeError, err.Error()
	case answer.Incomplete != "":
		entry.Outcome, entry.Incomplete = journalModel.OutcomeIncomplete, answer.Incomplete
	default:
		entry.Outcome = journalModel.OutcomeAnswered
	}
	if answer != nil {
		entry.Steps, entry.ToolCalls, entry.Charts = answer.Steps, answer.ToolCalls, len(answer.Charts)
		entry.InputTokens, entry.CachedTokens, entry.OutputTokens = answer.Usage.InputTokens, answer.Usage.CachedTokens, answer.Usage.OutputTokens
		entry.Tools = lo.Map(answer.Trace, func(t agentModel.ToolTrace, _ int) string { return t.Name })
		entry.Trace = lo.Map(answer.Trace, func(t agentModel.ToolTrace, _ int) journalModel.ToolCall {
			return journalModel.ToolCall{Step: t.Step, Name: t.Name, Arguments: t.Arguments, Status: t.Status, Output: t.Output, Duration: t.Duration}
		})
		entry.Answer = answer.ModelAnswer
	}

	if jerr := u.journal.Append(context.WithoutCancel(ctx), entry); jerr != nil {
		slog.Warn("journal append", "error", jerr)
	}
}

// Reset забывает историю беседы клиента.
func (u *Usecase) Reset(ctx context.Context, client, conversationId string) error {
	conversation := conversationKey(&model.Question{Client: client, ConversationId: conversationId})
	if conversation == "" {
		return fmt.Errorf("%w: empty conversation_id", errs.InvalidRequest)
	}
	if err := u.dialog.Reset(ctx, conversation); err != nil {
		return fmt.Errorf("dialog.Reset: %w", err)
	}
	return nil
}

// conversationKey — беседа в пространстве клиента: номера бесед разных систем не пересекаются.
func conversationKey(q *model.Question) string {
	id := strings.TrimSpace(q.ConversationId)
	if id == "" {
		return ""
	}
	return q.Client + "/" + id
}

func (u *Usecase) lock(conversation string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()

	if _, ok := u.busy[conversation]; ok {
		return false
	}
	u.busy[conversation] = struct{}{}
	return true
}

func (u *Usecase) unlock(conversation string) {
	u.mu.Lock()
	defer u.mu.Unlock()

	delete(u.busy, conversation)
}
