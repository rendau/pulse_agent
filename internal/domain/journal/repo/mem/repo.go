// Package mem — журнал вопросов в памяти процесса (без PG_DSN: локально, тесты): кольцо
// последних N записей.
package mem

import (
	"context"
	"sync"
	"time"

	"github.com/mechta-market/pulse_agent/internal/domain/journal/model"
)

type Repo struct {
	mu      sync.Mutex
	size    int
	lastId  int64
	entries []*model.Entry // старые — первыми
}

func New(size int) *Repo {
	return &Repo{size: max(size, 1)}
}

func (r *Repo) Append(_ context.Context, e *model.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.lastId++
	e.Id = r.lastId
	r.entries = append(r.entries, e)
	if len(r.entries) > r.size {
		r.entries = r.entries[len(r.entries)-r.size:]
	}
	return nil
}

// List — записи по фильтру, новые — первыми; копии без Answer и Trace, как у Postgres.
func (r *Repo) List(_ context.Context, f model.Filter) ([]*model.Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	result := make([]*model.Entry, 0)
	for i := len(r.entries) - 1; i >= 0; i-- {
		e := r.entries[i]
		if (f.Client != "" && e.Client != f.Client) || (f.Outcome != "" && e.Outcome != f.Outcome) ||
			(!f.Since.IsZero() && e.At.Before(f.Since)) || (f.BeforeId > 0 && e.Id >= f.BeforeId) {
			continue
		}
		brief := *e
		brief.Answer, brief.Trace = "", nil
		result = append(result, &brief)
		if f.Limit > 0 && len(result) >= f.Limit {
			break
		}
	}
	return result, nil
}

func (r *Repo) Get(_ context.Context, id int64) (*model.Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, e := range r.entries {
		if e.Id == id {
			return new(*e), nil
		}
	}
	return nil, nil
}

func (r *Repo) DeleteBefore(_ context.Context, before time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	kept := r.entries[:0]
	for _, e := range r.entries {
		if !e.At.Before(before) {
			kept = append(kept, e)
		}
	}
	deleted := int64(len(r.entries) - len(kept))
	r.entries = kept
	return deleted, nil
}
