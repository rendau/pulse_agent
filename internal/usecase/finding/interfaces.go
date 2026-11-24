package finding

import (
	"context"
	"time"

	notifyModel "github.com/rendau/pulse_agent/internal/domain/notify/model"
)

type NotifyServiceI interface {
	Recent(ctx context.Context, kind, key string, within time.Duration) (*notifyModel.Notification, error)
	Notify(ctx context.Context, n *notifyModel.Notification) error
}
