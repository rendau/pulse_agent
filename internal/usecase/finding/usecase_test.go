package finding

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	notifyModel "github.com/rendau/pulse_agent/internal/domain/notify/model"
	"github.com/rendau/pulse_agent/internal/errs"
	"github.com/rendau/pulse_agent/internal/usecase/finding/model"
)

// fakeNotify — уведомления в памяти: Recent — как notify.Service (вид, ключ, за within).
type fakeNotify struct {
	now           func() time.Time
	notifications []*notifyModel.Notification
}

func (n *fakeNotify) Recent(_ context.Context, kind, key string, within time.Duration) (*notifyModel.Notification, error) {
	found, _, ok := lo.FindLastIndexOf(n.notifications, func(x *notifyModel.Notification) bool {
		return x.Kind == kind && x.Key == key && !x.At.Before(n.now().Add(-within))
	})
	return lo.Ternary(ok, found, nil), nil
}

func (n *fakeNotify) Notify(_ context.Context, x *notifyModel.Notification) error {
	x.Id, x.At = int64(len(n.notifications)+1), n.now()
	n.notifications = append(n.notifications, x)
	return nil
}

func newTest(perHour int) (*Usecase, *fakeNotify, *time.Time) {
	clock := time.Date(2026, 11, 23, 19, 40, 0, 0, time.UTC)
	notify := &fakeNotify{now: func() time.Time { return clock }}
	u := New(Config{Repeat: 6 * time.Hour, PerHour: perHour}, notify)
	u.now = notify.now
	return u, notify, &clock
}

func finding(addr string) *model.Finding {
	return &model.Finding{Key: "security:" + addr, Kind: "security", Severity: "critical",
		Title: "атака с " + addr + ": перебор СМС-кода", Text: "- 15 390 попыток за 13 минут", Source: "claude-routine"}
}

func TestReport(t *testing.T) {
	u, notify, _ := newTest(10)
	res, err := u.Report(context.Background(), "pulse", finding("129.226.55.176"))
	require.NoError(t, err)
	assert.Equal(t, &model.Result{Id: 1}, res)

	n := notify.notifications[0]
	assert.Equal(t, notifyModel.KindSecurity, n.Kind)
	assert.Empty(t, n.Service)
	assert.Equal(t, "129.226.55.176", n.Key, "ключ — адрес, как у наблюдателя")
	assert.Equal(t, "critical", n.Severity)
	assert.Equal(t, "finding:pulse", n.SignalKey)
	assert.True(t, n.FromFinding())
	assert.True(t, strings.HasSuffix(n.Text, "_Источник: Claude, расписание_"), n.Text)
}

func TestReportDeduplicatedWithWatcher(t *testing.T) {
	u, notify, clock := newTest(10)
	// наблюдатель уже сообщил об адресе (разбор сигнала security:<адрес>:attack)
	require.NoError(t, notify.Notify(context.Background(), &notifyModel.Notification{
		Kind: notifyModel.KindSecurity, Key: "129.226.55.176", Severity: "critical", SignalKey: "security:129.226.55.176:attack"}))

	res, err := u.Report(context.Background(), "pulse", finding("129.226.55.176"))
	require.NoError(t, err)
	assert.Equal(t, &model.Result{Deduplicated: true}, res, "об адресе уже было уведомление наблюдателя")
	assert.Len(t, notify.notifications, 1)

	res, err = u.Report(context.Background(), "pulse", finding("45.9.9.9"))
	require.NoError(t, err)
	assert.Equal(t, int64(2), res.Id, "другой адрес — новое уведомление")

	res, err = u.Report(context.Background(), "pulse", finding("45.9.9.9"))
	require.NoError(t, err)
	assert.True(t, res.Deduplicated, "повтор находки")

	*clock = clock.Add(6*time.Hour + time.Minute)
	res, err = u.Report(context.Background(), "pulse", finding("129.226.55.176"))
	require.NoError(t, err)
	assert.Equal(t, int64(3), res.Id, "прошло больше WATCH_ALERT_REPEAT — снова")
}

func TestReportRateLimit(t *testing.T) {
	u, _, clock := newTest(2)
	for _, addr := range []string{"1.1.1.1", "2.2.2.2"} {
		_, err := u.Report(context.Background(), "pulse", finding(addr))
		require.NoError(t, err)
	}
	_, err := u.Report(context.Background(), "pulse", finding("3.3.3.3"))
	require.ErrorIs(t, err, errs.RateLimited)

	_, err = u.Report(context.Background(), "other", finding("3.3.3.3"))
	require.NoError(t, err, "лимит — на клиента")

	*clock = clock.Add(time.Hour)
	_, err = u.Report(context.Background(), "pulse", finding("4.4.4.4"))
	require.NoError(t, err, "через час — снова можно")
}

func TestReportCleanAndTruncate(t *testing.T) {
	u, notify, _ := newTest(10)
	f := finding("2a03:32c0:1:2::/64")
	f.Title = "атака\nс\x1b[31m адреса‮ " + strings.Repeat("я", 200)
	f.Text = "строка 1\r\nстрока\x00 2\t" + strings.Repeat("ж", 3000)
	f.Source = "Evil\nSource"
	_, err := u.Report(context.Background(), "pulse", f)
	require.NoError(t, err)

	n := notify.notifications[0]
	assert.Equal(t, 120, utf8.RuneCountInString(n.Title))
	assert.True(t, strings.HasPrefix(n.Title, "атака с[31m адреса яя"), n.Title)
	assert.True(t, strings.HasSuffix(n.Title, "…"))
	assert.NotContains(t, n.Title, "\x1b")
	assert.NotContains(t, n.Title, "‮")

	body, source, ok := strings.Cut(n.Text, "\n\n_Источник: ")
	require.True(t, ok)
	assert.Equal(t, 2000, utf8.RuneCountInString(body))
	assert.True(t, strings.HasPrefix(body, "строка 1\nстрока 2 жж"))
	assert.Equal(t, "внешняя находка (unknown)_", source, "источник не из словаря — без текста клиента")
	assert.Equal(t, "2a03:32c0:1:2::/64", n.Key)
}

func TestReportInvalid(t *testing.T) {
	u, notify, _ := newTest(10)
	cases := map[string]func(f *model.Finding){
		"kind":           func(f *model.Finding) { f.Kind = "alert" },
		"пустой kind":    func(f *model.Finding) { f.Kind = "" },
		"severity":       func(f *model.Finding) { f.Severity = "info" },
		"key без адреса": func(f *model.Finding) { f.Key = "security:not-an-ip" },
		"key без вида":   func(f *model.Finding) { f.Key = "129.226.55.176" },
		"пустой title":   func(f *model.Finding) { f.Title = " \x00 " },
	}
	for name, change := range cases {
		f := finding("129.226.55.176")
		change(f)
		_, err := u.Report(context.Background(), "pulse", f)
		require.ErrorIs(t, err, errs.InvalidRequest, name)
	}
	assert.Empty(t, notify.notifications)
}
