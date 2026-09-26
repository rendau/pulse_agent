// Package monitor — мониторинг агента для разработчика (/debug/*): последние вопросы, вопрос
// целиком (ответ и ход разбора), сводка по журналу, что за агент работает и видит ли он pulse.
package monitor

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"

	journalModel "github.com/rendau/pulse_agent/internal/domain/journal/model"
	"github.com/rendau/pulse_agent/internal/errs"
	pulseModel "github.com/rendau/pulse_agent/internal/service/pulse/model"
	"github.com/rendau/pulse_agent/internal/usecase/monitor/model"
)

const (
	// maxRecent — последних вопросов за запрос.
	maxRecent = 200
	// defaultStatsWindow — окно сводки по умолчанию.
	defaultStatsWindow = 7 * 24 * time.Hour
)

type JournalServiceI interface {
	Recent(ctx context.Context, f journalModel.Filter) ([]*journalModel.Entry, error)
	Get(ctx context.Context, id int64) (*journalModel.Entry, error)
	Stats(ctx context.Context, since time.Time) (*journalModel.Stats, error)
}

type PulseI interface {
	Catalog(ctx context.Context) (*pulseModel.Catalog, error)
}

type Usecase struct {
	info    model.Info // статичная часть
	journal JournalServiceI
	pulse   PulseI
	now     func() time.Time
}

func New(info model.Info, journal JournalServiceI, pulse PulseI) *Usecase {
	return &Usecase{info: info, journal: journal, pulse: pulse, now: time.Now}
}

// Recent — последние вопросы (новые — первыми), не больше maxRecent.
func (u *Usecase) Recent(ctx context.Context, f journalModel.Filter) ([]*journalModel.Entry, error) {
	f.Limit = lo.Ternary(f.Limit <= 0 || f.Limit > maxRecent, maxRecent, f.Limit)
	entries, err := u.journal.Recent(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("journal.Recent: %w", err)
	}
	return entries, nil
}

// Entry — вопрос целиком: ответ и ход разбора. Ошибки: errs.ObjectNotFound — нет такого (или
// удалён по сроку хранения).
func (u *Usecase) Entry(ctx context.Context, id int64) (*journalModel.Entry, error) {
	e, err := u.journal.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("journal.Get: %w", err)
	}
	if e == nil {
		return nil, fmt.Errorf("%w: journal entry %d", errs.ObjectNotFound, id)
	}
	return e, nil
}

// Stats — сводка за окно: window — «7d», «24h», «30m» (пусто — 7 дней, не больше срока хранения).
// Ошибки: errs.InvalidRequest — окно не разобрать.
func (u *Usecase) Stats(ctx context.Context, window string) (*journalModel.Stats, error) {
	d, err := parseWindow(window)
	if err != nil {
		return nil, err
	}
	if u.info.JournalRetention > 0 {
		d = min(d, u.info.JournalRetention)
	}
	stats, err := u.journal.Stats(ctx, u.now().Add(-d))
	if err != nil {
		return nil, fmt.Errorf("journal.Stats: %w", err)
	}
	return stats, nil
}

// parseWindow — длительность Go или число дней «7d»; пусто — defaultStatsWindow.
func parseWindow(window string) (time.Duration, error) {
	window = strings.TrimSpace(window)
	if window == "" {
		return defaultStatsWindow, nil
	}
	if days, ok := strings.CutSuffix(window, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%w: window %q; expected e.g. 7d, 24h, 30m", errs.InvalidRequest, window)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(window)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%w: window %q; expected e.g. 7d, 24h, 30m", errs.InvalidRequest, window)
	}
	return d, nil
}

// Info — сведения об агенте и доступность pulse (каталог инструментов, до 10 с).
func (u *Usecase) Info(ctx context.Context) *model.Info {
	info := u.info

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	catalog, err := u.pulse.Catalog(ctx)
	if err != nil {
		info.PulseError = err.Error()
		return &info
	}
	info.PulseTools = lo.Map(catalog.Tools, func(t pulseModel.Tool, _ int) string { return t.Name })
	return &info
}
