// Package mem — журнал вопросов в памяти процесса: кольцо последних N записей.
package mem

import (
	"context"
	"sync"

	"github.com/mechta-market/pulse_agent/internal/domain/journal/model"
)

type Repo struct {
	mu      sync.Mutex
	size    int
	entries []*model.Entry // старые — первыми
}

func New(size int) *Repo {
	return &Repo{size: max(size, 1)}
}

func (r *Repo) Append(_ context.Context, e *model.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.entries = append(r.entries, e)
	if len(r.entries) > r.size {
		r.entries = r.entries[len(r.entries)-r.size:]
	}
	return nil
}

// List — все записи, старые — первыми (копия среза, записи не меняются после Append).
func (r *Repo) List(_ context.Context) ([]*model.Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]*model.Entry{}, r.entries...), nil
}
