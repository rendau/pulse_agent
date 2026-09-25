// Package ask — вопрос агенту от системы-клиента: формат, «один вопрос за раз на беседу»,
// история беседы, агент, метрики по клиентам. Доступ проверяет транспорт (ключ API).
package ask

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/samber/lo"

	"github.com/mechta-market/pulse_agent/internal/constant"
	dialogModel "github.com/mechta-market/pulse_agent/internal/domain/dialog/model"
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
	dialog DialogServiceI
	agent  AgentI

	mu   sync.Mutex
	busy map[string]struct{} // беседы, где идёт разбор
}

func New(dialog DialogServiceI, agent AgentI) *Usecase {
	return &Usecase{dialog: dialog, agent: agent, busy: map[string]struct{}{}}
}

// Ask разбирает вопрос. Ошибки: errs.InvalidRequest — пустой или слишком длинный вопрос,
// неизвестный формат; errs.Busy — в беседе уже идёт разбор.
func (u *Usecase) Ask(ctx context.Context, q *model.Question) (*model.Answer, error) {
	text := strings.TrimSpace(q.Text)
	switch {
	case text == "":
		return nil, fmt.Errorf("%w: empty question", errs.InvalidRequest)
	case len([]rune(text)) > maxQuestionChars:
		return nil, fmt.Errorf("%w: question is longer than %d characters", errs.InvalidRequest, maxQuestionChars)
	}
	format := lo.CoalesceOrEmpty(q.Format, agentConstant.FormatTelegram)
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
		Question: text,
		Format:   format,
		Charts:   q.Charts,
	})
	if err != nil {
		return nil, fmt.Errorf("agent.Run: %w", err)
	}

	slog.Info("question answered",
		"client", q.Client,
		"conversation", q.ConversationId,
		"user", q.User.Id,
		"format", format,
		"steps", result.Steps,
		"tool_calls", result.ToolCalls,
		"incomplete", result.Incomplete,
		"input_tokens", result.Usage.InputTokens,
		"cached_tokens", result.Usage.CachedTokens,
		"output_tokens", result.Usage.OutputTokens,
		"charts", len(result.Charts),
	)

	// пустой ответ в историю не кладём: он только собьёт следующий вопрос
	if conversation != "" && result.Answer != "" {
		if err = u.dialog.Append(ctx, conversation, text, result.Answer); err != nil {
			return nil, fmt.Errorf("dialog.Append: %w", err)
		}
	}

	return &model.Answer{
		Text:       result.Answer,
		Structured: result.Structured,
		Incomplete: result.Incomplete,
		Charts:     result.Charts,
		Steps:      result.Steps,
		ToolCalls:  result.ToolCalls,
		Usage:      result.Usage,
		Trace:      result.Trace,
	}, nil
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
