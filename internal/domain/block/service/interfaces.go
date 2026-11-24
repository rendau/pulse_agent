package service

import (
	"context"

	"github.com/rendau/pulse_agent/internal/domain/block/model"
)

type RepoI interface {
	// ListDecisions — решения по уведомлению, по порядку.
	ListDecisions(ctx context.Context, notificationId int64) ([]*model.Decision, error)
	// CreateDecision сохраняет решение и проставляет Id; false — не сохранено: у уведомления уже
	// есть итоговое решение или (для skip) «не блокировать».
	CreateDecision(ctx context.Context, d *model.Decision) (bool, error)
}
