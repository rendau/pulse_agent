// Package db — журнал вопросов в Postgres (таблица journal).
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mechta-market/mobone/v2"
	"github.com/samber/lo"

	commonRepoPg "github.com/mechta-market/pulse_agent/internal/domain/common/repo/pg"
	"github.com/mechta-market/pulse_agent/internal/domain/journal/model"
	repoModel "github.com/mechta-market/pulse_agent/internal/domain/journal/repo/db/model"
)

const tableName = "journal"

type Repo struct {
	*commonRepoPg.Base
	ModelStore *mobone.ModelStore
}

func New(con *pgxpool.Pool) *Repo {
	base := commonRepoPg.NewBase(con)
	return &Repo{
		Base: base,
		ModelStore: &mobone.ModelStore{
			Con:       base.Con,
			QB:        base.QB,
			TableName: tableName,
		},
	}
}

func (r *Repo) Append(ctx context.Context, e *model.Entry) error {
	m, err := repoModel.DecodeUpsert(e)
	if err != nil {
		return fmt.Errorf("DecodeUpsert: %w", err)
	}
	if err = r.ModelStore.Create(ctx, m); err != nil {
		return fmt.Errorf("ModelStore.Create: %w", err)
	}
	e.Id = m.NewId
	return nil
}

func (r *Repo) List(ctx context.Context, f model.Filter) ([]*model.Entry, error) {
	conditions, conditionExps := r.getConditions(f)
	items := make([]*repoModel.Select, 0)

	_, err := r.ModelStore.List(ctx, mobone.ListParams{
		Conditions:           conditions,
		ConditionExpressions: conditionExps,
		Columns:              repoModel.BriefColumns,
		PageSize:             int64(max(f.Limit, 0)),
		Sort:                 []string{"id desc"},
	}, func(add bool) mobone.ListModelI {
		item := &repoModel.Select{}
		if add {
			items = append(items, item)
		}
		return item
	})
	if err != nil {
		return nil, fmt.Errorf("ModelStore.List: %w", err)
	}

	return lo.Map(items, repoModel.EncodeSelect), nil
}

func (r *Repo) Get(ctx context.Context, id int64) (*model.Entry, error) {
	m := &repoModel.Select{Id: id}
	found, err := r.ModelStore.Get(ctx, m)
	if err != nil {
		return nil, fmt.Errorf("ModelStore.Get: %w", err)
	}
	if !found {
		return nil, nil
	}
	return repoModel.EncodeSelect(m, 0), nil
}

func (r *Repo) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	query, args, err := r.QB.Delete(tableName).Where("at < ?", before).ToSql()
	if err != nil {
		return 0, fmt.Errorf("build query: %w", err)
	}
	tag, err := r.Con.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("Con.Exec: %w", err)
	}
	return tag.RowsAffected(), nil
}
