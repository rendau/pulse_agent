package ask

import (
	"context"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse_agent/internal/domain/dialog/repo/mem"
	dialogServiceP "github.com/mechta-market/pulse_agent/internal/domain/dialog/service"
	journalModel "github.com/mechta-market/pulse_agent/internal/domain/journal/model"
	journalMem "github.com/mechta-market/pulse_agent/internal/domain/journal/repo/mem"
	journalServiceP "github.com/mechta-market/pulse_agent/internal/domain/journal/service"
	"github.com/mechta-market/pulse_agent/internal/errs"
	agentModel "github.com/mechta-market/pulse_agent/internal/service/agent/model"
	piiServiceP "github.com/mechta-market/pulse_agent/internal/service/pii/service"
	"github.com/mechta-market/pulse_agent/internal/usecase/ask/model"
)

type fakeAgent struct {
	reqs    []*agentModel.Req
	result  *agentModel.Result
	started chan struct{}
	release chan struct{}
}

func (f *fakeAgent) Run(_ context.Context, req *agentModel.Req) (*agentModel.Result, error) {
	f.reqs = append(f.reqs, req)
	if f.started != nil {
		f.started <- struct{}{}
		<-f.release
	}
	// модель без персональных данных пишет то же, что уходит клиенту
	result := *f.result
	result.ModelAnswer = lo.CoalesceOrEmpty(result.ModelAnswer, result.Answer)
	return &result, nil
}

func newUsecase(agent *fakeAgent) *Usecase {
	dialog := dialogServiceP.New(dialogServiceP.Config{MaxTurns: 10, Ttl: time.Hour}, mem.New())
	return New(dialog, journalServiceP.New(journalMem.New(100)), agent, piiServiceP.New(piiServiceP.Config{Key: []byte("test")}))
}

func TestAsk_History(t *testing.T) {
	ctx := context.Background()
	agent := &fakeAgent{result: &agentModel.Result{Answer: "всё ок"}}
	uc := newUsecase(agent)

	ans, err := uc.Ask(ctx, &model.Question{Client: "bot", ConversationId: "1", Text: "  что с caravan?  ", Charts: true})
	require.NoError(t, err)
	assert.Equal(t, "всё ок", ans.Text)
	assert.Equal(t, "telegram", agent.reqs[0].Format, "формат по умолчанию")
	assert.True(t, agent.reqs[0].Charts)

	// второй вопрос беседы идёт с историей первого
	_, err = uc.Ask(ctx, &model.Question{Client: "bot", ConversationId: "1", Text: "а логи?"})
	require.NoError(t, err)
	assert.Equal(t, []agentModel.Turn{{Question: "что с caravan?", Answer: "всё ок"}}, agent.reqs[1].History)

	// беседа с тем же номером у другой системы — своя
	_, err = uc.Ask(ctx, &model.Question{Client: "service-desk", ConversationId: "1", Text: "q"})
	require.NoError(t, err)
	assert.Empty(t, agent.reqs[2].History)

	// без номера беседы — без истории и в историю не пишется
	_, err = uc.Ask(ctx, &model.Question{Client: "bot", Text: "q", Format: "json"})
	require.NoError(t, err)
	assert.Empty(t, agent.reqs[3].History)
	assert.Equal(t, "json", agent.reqs[3].Format)

	// после сброса истории нет
	require.NoError(t, uc.Reset(ctx, "bot", "1"))
	_, err = uc.Ask(ctx, &model.Question{Client: "bot", ConversationId: "1", Text: "ещё"})
	require.NoError(t, err)
	assert.Empty(t, agent.reqs[4].History)

	require.ErrorIs(t, uc.Reset(ctx, "bot", " "), errs.InvalidRequest)
}

func TestAsk_Validation(t *testing.T) {
	uc := newUsecase(&fakeAgent{result: &agentModel.Result{}})

	_, err := uc.Ask(context.Background(), &model.Question{Client: "bot", Text: "   "})
	require.ErrorIs(t, err, errs.InvalidRequest)

	_, err = uc.Ask(context.Background(), &model.Question{Client: "bot", Text: "q", Format: "html"})
	require.ErrorIs(t, err, errs.InvalidRequest)
	assert.ErrorContains(t, err, "format")

	long := make([]rune, maxQuestionChars+1)
	for i := range long {
		long[i] = 'я'
	}
	_, err = uc.Ask(context.Background(), &model.Question{Client: "bot", Text: string(long)})
	require.ErrorIs(t, err, errs.InvalidRequest)
}

func TestAsk_BusyConversation(t *testing.T) {
	agent := &fakeAgent{result: &agentModel.Result{Answer: "ok"}, started: make(chan struct{}), release: make(chan struct{})}
	uc := newUsecase(agent)

	done := make(chan error)
	go func() {
		_, err := uc.Ask(context.Background(), &model.Question{Client: "bot", ConversationId: "1", Text: "первый"})
		done <- err
	}()
	<-agent.started

	_, err := uc.Ask(context.Background(), &model.Question{Client: "bot", ConversationId: "1", Text: "второй"})
	require.ErrorIs(t, err, errs.Busy)

	close(agent.release)
	require.NoError(t, <-done)
}

func TestAsk_ResponseSchema(t *testing.T) {
	agent := &fakeAgent{result: &agentModel.Result{Answer: `{"a":1}`, Json: []byte(`{"a":1}`)}}
	uc := newUsecase(agent)
	schema := map[string]any{"type": "object"}

	ans, err := uc.Ask(context.Background(), &model.Question{Client: "sd", Text: "q", ResponseSchema: schema})
	require.NoError(t, err)
	assert.Equal(t, "json", agent.reqs[0].Format, "своя схема — формат json")
	assert.Equal(t, schema, agent.reqs[0].ResponseSchema)
	assert.JSONEq(t, `{"a":1}`, string(ans.Json))

	_, err = uc.Ask(context.Background(), &model.Question{Client: "sd", Text: "q", Format: "plain", ResponseSchema: schema})
	require.ErrorIs(t, err, errs.InvalidRequest)
}

func TestAsk_Journal(t *testing.T) {
	journal := journalServiceP.New(journalMem.New(10))
	agent := &fakeAgent{result: &agentModel.Result{Answer: "ok", Incomplete: "timeout", ToolCalls: 1,
		Trace: []agentModel.ToolTrace{{Step: 1, Name: "query_logs", Arguments: `{"pattern":"1"}`, Status: "ok", Output: "{}", Duration: time.Second}}}}
	uc := New(dialogServiceP.New(dialogServiceP.Config{}, mem.New()), journal, agent, piiServiceP.New(piiServiceP.Config{Key: []byte("test")}))

	_, err := uc.Ask(context.Background(), &model.Question{Client: "bot", User: model.User{Id: "7", Name: "Иван"}, Text: "что по клиенту +7 701 123 45 67?"})
	require.NoError(t, err)
	_, err = uc.Ask(context.Background(), &model.Question{Client: "bot", Text: " "})
	require.Error(t, err)

	entries, err := journal.Recent(context.Background(), journalModel.Filter{})
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, journalModel.OutcomeInvalid, entries[0].Outcome)
	assert.Equal(t, journalModel.OutcomeIncomplete, entries[1].Outcome)
	assert.Equal(t, "timeout", entries[1].Incomplete)
	assert.Equal(t, []string{"query_logs"}, entries[1].Tools)
	assert.Equal(t, "7", entries[1].UserId)
	assert.Empty(t, entries[1].Answer, "в списке — без ответа и хода разбора")

	full, err := journal.Get(context.Background(), entries[1].Id)
	require.NoError(t, err)
	require.NotNil(t, full)
	assert.Equal(t, "Иван", full.UserName)
	assert.Equal(t, "ok", full.Answer)
	assert.Regexp(t, `^что по клиенту pii:phone:[a-p]{12}\?$`, full.Question, "вопрос в журнале — токенами")
	assert.Equal(t, []journalModel.ToolCall{{Step: 1, Name: "query_logs", Arguments: `{"pattern":"1"}`, Status: "ok", Output: "{}", Duration: time.Second}}, full.Trace)
}
