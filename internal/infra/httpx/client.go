// Package httpx — единая фабрика http-клиентов. Клиент по умолчанию из net/http
// не годится: у него нет ни таймаута коннекта, ни лимита соединений на хост,
// поэтому недоступный или тормозящий upstream (LLM, pulse) подвешивает
// разбор до общего дедлайна.
package httpx

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

const (
	DefaultDialTimeout           = 2 * time.Second
	DefaultTLSHandshakeTimeout   = 2 * time.Second
	DefaultResponseHeaderTimeout = 30 * time.Second
	DefaultMaxConnsPerHost       = 20
	DefaultIdleConnTimeout       = 30 * time.Second
)

// Config — параметры клиента; нулевые поля заменяются дефолтами.
type Config struct {
	// Timeout — общий таймаут запроса вместе с чтением тела. Ноль — без него:
	// для потоковых ответов ограничение ставит контекст вызова.
	Timeout time.Duration

	DialTimeout           time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	MaxConnsPerHost       int

	// VerifyTLS включает проверку сертификата. По умолчанию выключена:
	// внутренние сервисы ходят по самоподписанным сертификатам.
	VerifyTLS bool
}

// New создаёт http-клиент с настроенным транспортом.
func New(cfg Config) *http.Client {
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = DefaultDialTimeout
	}
	if cfg.TLSHandshakeTimeout == 0 {
		cfg.TLSHandshakeTimeout = DefaultTLSHandshakeTimeout
	}
	if cfg.ResponseHeaderTimeout == 0 {
		cfg.ResponseHeaderTimeout = DefaultResponseHeaderTimeout
	}
	if cfg.MaxConnsPerHost == 0 {
		cfg.MaxConnsPerHost = DefaultMaxConnsPerHost
	}

	return &http.Client{
		Timeout: cfg.Timeout,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   cfg.DialTimeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   cfg.TLSHandshakeTimeout,
			ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: !cfg.VerifyTLS}, //nolint:gosec // внутренние сервисы с самоподписанными сертификатами
			MaxConnsPerHost:       cfg.MaxConnsPerHost,
			MaxIdleConnsPerHost:   cfg.MaxConnsPerHost,
			IdleConnTimeout:       DefaultIdleConnTimeout,
		},
	}
}
