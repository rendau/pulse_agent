package block

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	blockModel "github.com/rendau/pulse_agent/internal/domain/block/model"
	domainBlockService "github.com/rendau/pulse_agent/internal/domain/block/service"
	notifyModel "github.com/rendau/pulse_agent/internal/domain/notify/model"
	"github.com/rendau/pulse_agent/internal/errs"
	rutoblockModel "github.com/rendau/pulse_agent/internal/service/rutoblock/model"
	"github.com/rendau/pulse_agent/internal/usecase/block/model"
)

type fakeNotify struct {
	notifications map[int64]*notifyModel.Notification
}

func (f *fakeNotify) Notification(_ context.Context, id int64) (*notifyModel.Notification, error) {
	if n, ok := f.notifications[id]; ok {
		return n, nil
	}
	return nil, fmt.Errorf("%w: notification %d", errs.ObjectNotFound, id)
}

// fakeRepo — решения в памяти с уникальностью как у индексов block_decision: одно итоговое и одно
// «не блокировать» на уведомление.
type fakeRepo struct {
	mu        sync.Mutex
	decisions []*blockModel.Decision
}

func (r *fakeRepo) ListDecisions(_ context.Context, notificationId int64) ([]*blockModel.Decision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return lo.Filter(r.decisions, func(d *blockModel.Decision, _ int) bool { return d.NotificationId == notificationId }), nil
}

func (r *fakeRepo) CreateDecision(_ context.Context, d *blockModel.Decision) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	conflict := lo.ContainsBy(r.decisions, func(x *blockModel.Decision) bool {
		return x.NotificationId == d.NotificationId && (x.Final() && d.Final() || x.Action == blockModel.ActionSkip && d.Action == blockModel.ActionSkip)
	})
	if conflict {
		return false, nil
	}
	d.Id = int64(len(r.decisions) + 1)
	r.decisions = append(r.decisions, d)
	return true, nil
}

type fakeRuto struct {
	calls atomic.Int32
	reqs  []*rutoblockModel.BlockReq
	mu    sync.Mutex
	// block — ответ ruto; nil — блок как у агента до now+Duration
	block func(req *rutoblockModel.BlockReq) (*rutoblockModel.Block, error)
	delay time.Duration
}

func (f *fakeRuto) Block(ctx context.Context, req *rutoblockModel.BlockReq) (*rutoblockModel.Block, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	time.Sleep(f.delay)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if f.block != nil {
		return f.block(req)
	}
	return &rutoblockModel.Block{Id: "b1", Ip: req.Ip + "/32", Source: "agent", Active: true, ExpiresAt: expires(req.Duration)}, nil
}

var clock = time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

func expires(d time.Duration) time.Time {
	return clock.Add(d).Truncate(time.Second)
}

type testEnv struct {
	u    *Usecase
	repo *fakeRepo
	ruto *fakeRuto
}

func newTest() *testEnv {
	notify := &fakeNotify{notifications: map[int64]*notifyModel.Notification{
		1: {Id: 1, Kind: notifyModel.KindSecurity, Key: "203.0.113.7", Severity: "critical", Title: "атака с 203.0.113.7: перебор СМС-кода"},
		2: {Id: 2, Kind: notifyModel.KindDeploy, Service: "caravan", Key: "cac1a18", Title: "caravan: после выкатки сбои"},
		3: {Id: 3, Kind: notifyModel.KindSecurity, Key: "2a03:32c0:1:2::/64", Title: "разведка с сети"},
		4: {Id: 4, Kind: notifyModel.KindSecurity, Key: "not-an-ip", Title: "странный ключ"},
	}}
	repo := &fakeRepo{}
	blockService := domainBlockService.New(repo)
	ruto := &fakeRuto{}
	return &testEnv{u: New(notify, blockService, ruto), repo: repo, ruto: ruto}
}

func req(id int64, action, duration, chat string) *model.Request {
	return &model.Request{
		Client: "pulse_bot", ConversationId: chat, NotificationId: id, Action: action, Duration: duration,
		User: model.User{Id: "42", Name: "Иван Иванов"},
	}
}

func TestDecide_Block(t *testing.T) {
	env := newTest()
	res, err := env.u.Decide(context.Background(), req(1, "block", "24h", "tg:-100"))
	require.NoError(t, err)
	assert.False(t, res.Repeat)
	d := res.Decision
	assert.Equal(t, "block", d.Action)
	assert.Equal(t, "203.0.113.7/32", d.Ip, "сеть — как записал ruto")
	require.NotNil(t, d.Until)
	assert.Equal(t, expires(24*time.Hour), *d.Until)
	assert.Equal(t, "b1", d.RutoBlockId)
	assert.Empty(t, d.Refused)
	assert.Equal(t, "42", d.ById)
	assert.Equal(t, "Иван Иванов", d.ByName)
	assert.Equal(t, "tg:-100", d.ConversationId)
	assert.Equal(t, "pulse_bot", d.Client)
	assert.False(t, d.At.IsZero())

	require.Len(t, env.ruto.reqs, 1)
	r := env.ruto.reqs[0]
	assert.Equal(t, "203.0.113.7", r.Ip)
	assert.Equal(t, 24*time.Hour, r.Duration)
	assert.Equal(t, "pulse: атака с 203.0.113.7: перебор СМС-кода — решение в Telegram: Иван Иванов (42)", r.Reason)
}

func TestDecide_Block7dNetwork(t *testing.T) {
	env := newTest()
	env.ruto.block = func(req *rutoblockModel.BlockReq) (*rutoblockModel.Block, error) {
		return &rutoblockModel.Block{Id: "b2", Ip: "2a03:32c0:1:2::/64", Source: "agent", ExpiresAt: expires(req.Duration)}, nil
	}
	res, err := env.u.Decide(context.Background(), req(3, "block", "168h", "tg:-100"))
	require.NoError(t, err)
	assert.Equal(t, 7*24*time.Hour, env.ruto.reqs[0].Duration)
	assert.Equal(t, "2a03:32c0:1:2::/64", env.ruto.reqs[0].Ip, "сеть — как в ключе уведомления")
	assert.Equal(t, expires(7*24*time.Hour), *res.Decision.Until)
}

func TestDecide_FinalRepeat(t *testing.T) {
	env := newTest()
	first, err := env.u.Decide(context.Background(), req(1, "block", "24h", "tg:-100"))
	require.NoError(t, err)

	// второй админ из другого чата — другой срок, «не блокировать»: тот же блок, ruto не зовётся
	for _, r := range []*model.Request{req(1, "block", "168h", "tg:-200"), req(1, "skip", "", "tg:-200"), req(1, "block", "24h", "tg:-100")} {
		r.User = model.User{Id: "7", Name: "Пётр"}
		res, err := env.u.Decide(context.Background(), r)
		require.NoError(t, err)
		assert.True(t, res.Repeat, r.Action+" "+r.Duration)
		assert.Equal(t, first.Decision, res.Decision, "прежнее решение — с прежним админом")
	}
	assert.Equal(t, int32(1), env.ruto.calls.Load())
	assert.Len(t, env.repo.decisions, 1)
}

func TestDecide_SkipThenBlock(t *testing.T) {
	env := newTest()
	skip, err := env.u.Decide(context.Background(), req(1, "skip", "", "tg:-100"))
	require.NoError(t, err)
	assert.False(t, skip.Repeat)
	assert.Equal(t, "skip", skip.Decision.Action)
	assert.Equal(t, "203.0.113.7", skip.Decision.Ip, "skip — ключ уведомления")
	assert.Nil(t, skip.Decision.Until)
	assert.Zero(t, env.ruto.calls.Load(), "skip ruto не зовёт")

	again := req(1, "skip", "", "tg:-200")
	again.User = model.User{Id: "7", Name: "Пётр"}
	res, err := env.u.Decide(context.Background(), again)
	require.NoError(t, err)
	assert.True(t, res.Repeat)
	assert.Equal(t, skip.Decision, res.Decision, "повторный skip — первый")

	block, err := env.u.Decide(context.Background(), req(1, "block", "24h", "tg:-200"))
	require.NoError(t, err)
	assert.False(t, block.Repeat, "skip не итоговое — блокировать после него можно")
	assert.Equal(t, "block", block.Decision.Action)
	assert.Equal(t, int32(1), env.ruto.calls.Load())

	res, err = env.u.Decide(context.Background(), req(1, "skip", "", "tg:-100"))
	require.NoError(t, err)
	assert.True(t, res.Repeat)
	assert.Equal(t, block.Decision, res.Decision, "после блока skip — тот блок")
}

func TestDecide_Refused(t *testing.T) {
	env := newTest()
	msg := "network 203.0.113.7/32: a person unblocked it on 2026-11-20 (ложное срабатывание)"
	env.ruto.block = func(*rutoblockModel.BlockReq) (*rutoblockModel.Block, error) {
		return nil, &rutoblockModel.RefusedError{Message: msg}
	}
	res, err := env.u.Decide(context.Background(), req(1, "block", "168h", "tg:-100"))
	require.NoError(t, err, "отказ ruto — не ошибка")
	assert.False(t, res.Repeat)
	assert.Equal(t, msg, res.Decision.Refused)
	assert.Equal(t, "203.0.113.7", res.Decision.Ip)
	assert.Nil(t, res.Decision.Until)
	assert.False(t, res.Decision.Final())

	// отказ — не итоговое: другой срок пробуется снова
	env.ruto.block = nil
	res, err = env.u.Decide(context.Background(), req(1, "block", "24h", "tg:-100"))
	require.NoError(t, err)
	assert.False(t, res.Repeat)
	assert.Empty(t, res.Decision.Refused)
	assert.Equal(t, int32(2), env.ruto.calls.Load())
	assert.Len(t, env.repo.decisions, 2, "попытка с отказом записана")
}

func TestDecide_AdminBlockWithoutEnd(t *testing.T) {
	env := newTest()
	env.ruto.block = func(*rutoblockModel.BlockReq) (*rutoblockModel.Block, error) {
		return &rutoblockModel.Block{Id: "h1", Ip: "203.0.113.0/24", Source: "admin", Active: true}, nil
	}
	res, err := env.u.Decide(context.Background(), req(1, "block", "24h", "tg:-100"))
	require.NoError(t, err)
	assert.True(t, res.Decision.Final(), "сеть уже заблокировал человек — это блок")
	assert.Nil(t, res.Decision.Until, "без конца")
	assert.Equal(t, "203.0.113.0/24", res.Decision.Ip)
	assert.Equal(t, "admin", res.Decision.RutoSource)
}

func TestDecide_RutoDown(t *testing.T) {
	env := newTest()
	env.ruto.block = func(*rutoblockModel.BlockReq) (*rutoblockModel.Block, error) {
		return nil, fmt.Errorf("%w: http://ruto-core.default.svc/api/blocked_client: status 502", errs.ServiceNA)
	}
	_, err := env.u.Decide(context.Background(), req(1, "block", "24h", "tg:-100"))
	require.ErrorIs(t, err, errs.ServiceNA)
	assert.Empty(t, env.repo.decisions, "ruto не ответил — ничего не записано")

	env.ruto.block = nil
	res, err := env.u.Decide(context.Background(), req(1, "block", "24h", "tg:-100"))
	require.NoError(t, err)
	assert.False(t, res.Repeat, "после сбоя можно снова")
}

func TestDecide_Invalid(t *testing.T) {
	env := newTest()
	cases := map[string]*model.Request{
		"не security":      req(2, "block", "24h", "tg:-100"),
		"ключ — не адрес":  req(4, "block", "24h", "tg:-100"),
		"action":           req(1, "ban", "24h", "tg:-100"),
		"пустой action":    req(1, "", "", "tg:-100"),
		"duration":         req(1, "block", "1h", "tg:-100"),
		"duration 7d":      req(1, "block", "7d", "tg:-100"),
		"без duration":     req(1, "block", "", "tg:-100"),
		"skip с duration":  req(1, "skip", "24h", "tg:-100"),
		"без беседы":       req(1, "block", "24h", " "),
		"id":               req(0, "block", "24h", "tg:-100"),
		"без пользователя": {Client: "pulse_bot", ConversationId: "tg:-100", NotificationId: 1, Action: "skip"},
	}
	for name, r := range cases {
		_, err := env.u.Decide(context.Background(), r)
		require.ErrorIs(t, err, errs.InvalidRequest, name)
	}
	_, err := env.u.Decide(context.Background(), req(99, "block", "24h", "tg:-100"))
	require.ErrorIs(t, err, errs.ObjectNotFound, "уведомления нет (удалено чисткой)")

	assert.Zero(t, env.ruto.calls.Load())
	assert.Empty(t, env.repo.decisions)
}

func TestDecide_Concurrent(t *testing.T) {
	env := newTest()
	env.ruto.delay = 50 * time.Millisecond

	results := make([]*model.Result, 4)
	var g errgroup.Group
	for i := range results {
		g.Go(func() error {
			r := req(1, lo.Ternary(i%2 == 0, "block", "skip"), lo.Ternary(i%2 == 0, lo.Ternary(i == 0, "24h", "168h"), ""), fmt.Sprintf("tg:-%d", i))
			res, err := env.u.Decide(context.Background(), r)
			results[i] = res
			return err
		})
	}
	require.NoError(t, g.Wait())

	blocks := lo.Filter(results, func(r *model.Result, _ int) bool { return r.Decision.Final() })
	assert.LessOrEqual(t, env.ruto.calls.Load(), int32(1), "одновременные нажатия — один вызов ruto")
	require.Len(t, lo.UniqBy(blocks, func(r *model.Result) int64 { return r.Decision.Id }), 1, "одно итоговое решение")
	assert.Len(t, lo.Filter(env.repo.decisions, func(d *blockModel.Decision, _ int) bool { return d.Final() }), 1)
	assert.Equal(t, 1, lo.CountBy(blocks, func(r *model.Result) bool { return !r.Repeat }), "новое — только одно")

	// по разным уведомлениям — параллельно, не ждут друг друга
	env.ruto.delay = 200 * time.Millisecond
	started := time.Now()
	g = errgroup.Group{}
	for _, id := range []int64{3, 1} {
		g.Go(func() error {
			_, err := env.u.Decide(context.Background(), req(id, "block", "24h", "tg:-100"))
			return err
		})
	}
	require.NoError(t, g.Wait())
	assert.Less(t, time.Since(started), 400*time.Millisecond)
}

// другая реплика успела записать итоговое решение, пока шёл вызов ruto: возвращается её решение
func TestDecide_OtherReplicaWon(t *testing.T) {
	env := newTest()
	other := &blockModel.Decision{NotificationId: 1, Action: "block", Duration: "168h", Ip: "203.0.113.7/32",
		ByName: "Пётр", Client: "pulse_bot", ConversationId: "tg:-200", At: clock}
	env.ruto.block = func(req *rutoblockModel.BlockReq) (*rutoblockModel.Block, error) {
		_, err := env.repo.CreateDecision(context.Background(), other)
		require.NoError(t, err)
		return &rutoblockModel.Block{Id: "b1", Ip: "203.0.113.7/32", Source: "agent", ExpiresAt: expires(req.Duration)}, nil
	}
	res, err := env.u.Decide(context.Background(), req(1, "block", "24h", "tg:-100"))
	require.NoError(t, err)
	assert.True(t, res.Repeat)
	assert.Equal(t, other, res.Decision)
	assert.Len(t, env.repo.decisions, 1)
}

func TestDecide_ClientGoneDuringRutoCall(t *testing.T) {
	env := newTest()
	env.ruto.delay = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)

	res, err := env.u.Decide(ctx, req(1, "block", "24h", "tg:-100"))
	require.NoError(t, err, "блок в ruto доводится и записывается, даже если бот ушёл")
	assert.True(t, res.Decision.Final())
	assert.Len(t, env.repo.decisions, 1)
}

func TestReason(t *testing.T) {
	n := &notifyModel.Notification{Title: "атака  с\n203.0.113.7"}
	assert.Equal(t, "pulse: атака с 203.0.113.7 — решение в Telegram: Иван (42)", reason(n, model.User{Id: "42", Name: "Иван"}))
	assert.Equal(t, "pulse: атака с 203.0.113.7 — решение в Telegram: 42", reason(n, model.User{Id: "42"}))
	assert.Equal(t, "pulse: атака с 203.0.113.7 — решение в Telegram: Иван", reason(n, model.User{Name: "Иван"}))

	n.Title = strings.Repeat("я", 600)
	r := reason(n, model.User{Id: "42", Name: "Иван"})
	assert.Equal(t, maxReason, utf8.RuneCountInString(r))
	assert.True(t, strings.HasSuffix(r, "…"+" — решение в Telegram: Иван (42)"), "обрезается заголовок, админ остаётся")

	r = reason(&notifyModel.Notification{Title: "x"}, model.User{Name: strings.Repeat("ж", 600)})
	assert.Equal(t, maxReason, utf8.RuneCountInString(r))
}
