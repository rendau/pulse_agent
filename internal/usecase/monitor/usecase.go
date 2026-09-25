// Package monitor — мониторинг агента для разработчика (/debug/*): последние вопросы, сводка по
// журналу, что за агент работает и видит ли он pulse.
package monitor

import (
	"context"
	"fmt"
	"time"

	"github.com/samber/lo"

	journalModel "github.com/mechta-market/pulse_agent/internal/domain/journal/model"
	pulseModel "github.com/mechta-market/pulse_agent/internal/service/pulse/model"
	"github.com/mechta-market/pulse_agent/internal/usecase/monitor/model"
)

// maxRecent — последних вопросов за запрос.
const maxRecent = 200

type JournalServiceI interface {
	Recent(ctx context.Context, f journalModel.Filter) ([]*journalModel.Entry, error)
	Stats(ctx context.Context) (*journalModel.Stats, error)
}

type PulseI interface {
	Catalog(ctx context.Context) (*pulseModel.Catalog, error)
}

type Usecase struct {
	info    model.Info // статичная часть
	journal JournalServiceI
	pulse   PulseI
}

func New(info model.Info, journal JournalServiceI, pulse PulseI) *Usecase {
	return &Usecase{info: info, journal: journal, pulse: pulse}
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

func (u *Usecase) Stats(ctx context.Context) (*journalModel.Stats, error) {
	stats, err := u.journal.Stats(ctx)
	if err != nil {
		return nil, fmt.Errorf("journal.Stats: %w", err)
	}
	return stats, nil
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
