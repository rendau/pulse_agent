package pulse

import (
	"context"

	pulseModel "github.com/rendau/pulse_agent/internal/service/pulse/model"
)

// Pulse — MCP-клиент сервера pulse.
type Pulse interface {
	// Catalog — актуальный список инструментов и instructions pulse
	// (перечитывается на каждый разбор: схемы pulse меняются по мере развития).
	Catalog(ctx context.Context) (*pulseModel.Catalog, error)

	// Call вызывает инструмент; arguments — JSON-объект от модели.
	Call(ctx context.Context, name string, arguments string) (*pulseModel.CallResult, error)

	Close() error
}
