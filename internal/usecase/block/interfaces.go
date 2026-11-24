package block

import (
	"context"

	blockModel "github.com/rendau/pulse_agent/internal/domain/block/model"
	notifyModel "github.com/rendau/pulse_agent/internal/domain/notify/model"
	rutoblockModel "github.com/rendau/pulse_agent/internal/service/rutoblock/model"
)

type NotifyServiceI interface {
	Notification(ctx context.Context, id int64) (*notifyModel.Notification, error)
}

type BlockServiceI interface {
	Decisions(ctx context.Context, notificationId int64) ([]*blockModel.Decision, error)
	Record(ctx context.Context, d *blockModel.Decision) (*blockModel.Decision, bool, error)
}

type RutoI interface {
	Block(ctx context.Context, req *rutoblockModel.BlockReq) (*rutoblockModel.Block, error)
}
