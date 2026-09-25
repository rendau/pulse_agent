package retention

import "context"

// Retention — фоновое удаление записей журнала старше срока хранения.
type Retention interface {
	// Run удаляет устаревшие записи один раз.
	Run(ctx context.Context) error
	// Start запускает периодическую чистку; Wait ждёт остановки после отмены ctx.
	Start(ctx context.Context)
	Wait()
}
