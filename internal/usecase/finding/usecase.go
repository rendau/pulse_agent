// Package finding — приём внешних находок об атаках (POST /v1/findings): расписание Claude
// смотрит журнал traefik через pulse и присылает найденное, pulse пересылает сюда. Находка
// становится уведомлением security сразу, без разбора моделью, и идёт той же лентой, что
// сигналы наблюдателя (только чатам с явной подпиской kind=security). Об адресе, о котором уже
// было уведомление security (наблюдатель или находка) за Repeat, второе не создаётся.
package finding

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/samber/lo"

	notifyModel "github.com/rendau/pulse_agent/internal/domain/notify/model"
	"github.com/rendau/pulse_agent/internal/errs"
	"github.com/rendau/pulse_agent/internal/usecase/finding/model"
)

const (
	maxTitle  = 120
	maxText   = 2000
	maxSource = 40
	keyPrefix = notifyModel.KindSecurity + ":"
	// SourceClaudeRoutine — расписание Claude
	SourceClaudeRoutine = "claude-routine"
)

var reSource = regexp.MustCompile(`^[a-z0-9_.-]+$`)

// sourceLabels — как источник показать людям.
var sourceLabels = map[string]string{SourceClaudeRoutine: "Claude, расписание"}

type Config struct {
	// Repeat — об одном адресе не чаще (WATCH_ALERT_REPEAT, как у сигналов наблюдателя)
	Repeat time.Duration
	// PerHour — находок в час от одного клиента (принятых и отброшенных как повтор)
	PerHour int
}

type Usecase struct {
	cfg    Config
	notify NotifyServiceI
	now    func() time.Time

	mu   sync.Mutex             // проверка повтора и создание — одно за раз; лимит
	seen map[string][]time.Time // клиент → время находок за последний час (в памяти)
}

func New(cfg Config, notify NotifyServiceI) *Usecase {
	return &Usecase{cfg: cfg, notify: notify, now: time.Now, seen: map[string][]time.Time{}}
}

// Report принимает находку клиента. Ошибки: errs.InvalidRequest — неверные поля;
// errs.RateLimited — больше PerHour находок за час.
func (u *Usecase) Report(ctx context.Context, client string, f *model.Finding) (*model.Result, error) {
	addr, n, err := notification(client, f)
	if err != nil {
		return nil, err
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	now := u.now()
	recent := lo.Filter(u.seen[client], func(t time.Time, _ int) bool { return now.Sub(t) < time.Hour })
	if u.cfg.PerHour > 0 && len(recent) >= u.cfg.PerHour {
		u.seen[client] = recent
		return nil, fmt.Errorf("%w: more than %d findings per hour", errs.RateLimited, u.cfg.PerHour)
	}
	u.seen[client] = append(recent, now)

	last, err := u.notify.Recent(ctx, notifyModel.KindSecurity, addr, u.cfg.Repeat)
	if err != nil {
		return nil, fmt.Errorf("notify.Recent: %w", err)
	}
	if last != nil {
		return &model.Result{Deduplicated: true}, nil
	}
	if err = u.notify.Notify(ctx, n); err != nil {
		return nil, fmt.Errorf("notify.Notify: %w", err)
	}
	return &model.Result{Id: n.Id}, nil
}

// notification — проверка полей и уведомление из находки: адрес — ключ (как у наблюдателя),
// заголовок и текст без управляющих символов и обрезаны, источник — в конце текста.
func notification(client string, f *model.Finding) (string, *notifyModel.Notification, error) {
	kind, severity := strings.TrimSpace(f.Kind), strings.TrimSpace(f.Severity)
	switch {
	case kind != notifyModel.KindSecurity:
		return "", nil, fmt.Errorf("%w: kind %q; expected security", errs.InvalidRequest, f.Kind)
	case severity != "warning" && severity != "critical":
		return "", nil, fmt.Errorf("%w: severity %q; expected warning or critical", errs.InvalidRequest, f.Severity)
	}
	addr, ok := strings.CutPrefix(strings.TrimSpace(f.Key), keyPrefix)
	if !ok || !validAddr(addr) {
		return "", nil, fmt.Errorf("%w: key %q; expected security:<ip>", errs.InvalidRequest, f.Key)
	}
	title := truncate(strings.Join(strings.Fields(clean(f.Title)), " "), maxTitle)
	if title == "" {
		return "", nil, fmt.Errorf("%w: title is required", errs.InvalidRequest)
	}
	source := strings.ToLower(strings.TrimSpace(f.Source))
	if source == "" || len(source) > maxSource || !reSource.MatchString(source) {
		source = "unknown"
	}

	text := truncate(strings.TrimSpace(clean(f.Text)), maxText)
	text = strings.TrimSpace(text + "\n\n_Источник: " + lo.CoalesceOrEmpty(sourceLabels[source], "внешняя находка ("+source+")") + "_")
	return addr, &notifyModel.Notification{
		Kind: notifyModel.KindSecurity, Key: addr, Severity: severity, Title: title, Text: text,
		Investigated: true, SignalKey: notifyModel.FindingSignalPrefix + client,
	}, nil
}

// validAddr — IPv4/IPv6 или подсеть.
func validAddr(addr string) bool {
	if _, err := netip.ParseAddr(addr); err == nil {
		return true
	}
	_, err := netip.ParsePrefix(addr)
	return err == nil
}

// clean — без управляющих символов (переводы строк остаются, табуляция — пробел).
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\t':
			return ' '
		case unicode.IsControl(r) || r == ' ' || r == ' ' || unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, strings.ReplaceAll(s, "\r\n", "\n"))
}

// truncate — не длиннее max символов, обрезанное — с «…».
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}
