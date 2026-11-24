package rutoblock

import (
	"context"

	rutoblockModel "github.com/rendau/pulse_agent/internal/service/rutoblock/model"
)

// Client — чёрный список клиентов API-шлюза ruto: блок адреса доходит до всех шлюзов за
// секунды, заблокированный получает 403 на любой ручке. Ключ ruto со scope blocklist.
type Client interface {
	// Block — POST /api/blocked_client; отказ ruto по его правилам — *rutoblockModel.RefusedError
	// (errors.Is(err, rutoblockModel.ErrRefused)); ruto недоступен, ответил 5xx или не принял
	// ключ — errs.ServiceNA.
	Block(ctx context.Context, req *rutoblockModel.BlockReq) (*rutoblockModel.Block, error)
}
