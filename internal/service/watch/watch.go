package watch

import "context"

// Watch — наблюдатель: сам замечает выкатки и алерты в pulse, разбирает их агентом и кладёт
// уведомления в ленту (беседы клиентов забирают её через API).
type Watch interface {
	// Start запускает опрос; Wait ждёт остановки после отмены ctx.
	Start(ctx context.Context)
	Wait()
}
