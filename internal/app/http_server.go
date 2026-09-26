package app

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/rendau/pulse_agent/internal/config"
	handlerHttpP "github.com/rendau/pulse_agent/internal/handler/http"
)

// HttpServerCreate строит сервер API. Разбор идёт до AGENT_TIMEOUT, прогон эталонов — до
// EVAL_TIMEOUT: таймаут записи — с запасом поверх большего; запросы живут в ctx приложения,
// чтобы остановка отменяла идущие разборы.
func HttpServerCreate(port string, handler *handlerHttpP.Handler, ctx context.Context) *http.Server {
	mux := http.NewServeMux()
	handler.Register(mux)

	return &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      max(config.Conf.AgentTimeout, config.Conf.EvalTimeout) + 30*time.Second,
		IdleTimeout:       time.Minute,
		MaxHeaderBytes:    64 * 1024,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
}
