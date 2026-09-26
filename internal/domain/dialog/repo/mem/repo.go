// Package mem — история диалогов в памяти процесса (v1: без БД, рестарт пода
// историю сбрасывает).
package mem

import (
	"context"
	"slices"
	"sync"

	"github.com/rendau/pulse_agent/internal/domain/dialog/model"
)

type Repo struct {
	mu    sync.Mutex
	chats map[string][]*model.Turn
}

func New() *Repo {
	return &Repo{chats: map[string][]*model.Turn{}}
}

func (r *Repo) List(_ context.Context, conversationId string) ([]*model.Turn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.chats[conversationId]), nil
}

func (r *Repo) Replace(_ context.Context, conversationId string, turns []*model.Turn) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(turns) == 0 {
		delete(r.chats, conversationId)
		return nil
	}

	r.chats[conversationId] = slices.Clone(turns)
	return nil
}
