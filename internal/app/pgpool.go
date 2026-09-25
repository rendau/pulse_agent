package app

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// initPgPool — пул журнала: пишет один вопрос за раз на беседу, читают ручки разработчика —
// соединений нужно мало (pulse-pg общий с pulse, max_connections=25).
func initPgPool(dsn string) (*pgxpool.Pool, error) {
	pgConf, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.ParseConfig: %w", err)
	}

	pgConf.MaxConns = 3
	pgConf.MinConns = 1
	pgConf.MaxConnLifetime = time.Hour
	pgConf.MaxConnIdleTime = 5 * time.Minute
	pgConf.HealthCheckPeriod = 15 * time.Second

	pgpool, err := pgxpool.NewWithConfig(context.Background(), pgConf)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.NewWithConfig: %w", err)
	}

	return pgpool, nil
}
