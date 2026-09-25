package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgInvalidCatalogName — SQLSTATE «базы нет».
const pgInvalidCatalogName = "3D000"

// ensureDatabase создаёт базу из DSN, если её нет: журнал живёт в Postgres другого сервиса
// (pulse-pg), и заводить базу руками при первой выкатке не нужно. Нужно право CREATEDB.
func ensureDatabase(dsn string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conf, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("pgx.ParseConfig: %w", err)
	}

	con, err := pgx.ConnectConfig(ctx, conf)
	if err == nil {
		return con.Close(ctx)
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != pgInvalidCatalogName {
		return fmt.Errorf("pgx.ConnectConfig: %w", err)
	}

	database := conf.Database
	conf.Database = "postgres"
	con, err = pgx.ConnectConfig(ctx, conf)
	if err != nil {
		return fmt.Errorf("pgx.ConnectConfig postgres: %w", err)
	}

	if _, err = con.Exec(ctx, "create database "+pgx.Identifier{database}.Sanitize()); err != nil {
		_ = con.Close(ctx)
		return fmt.Errorf("create database %s: %w", database, err)
	}
	slog.Info("database created", "database", database)
	return con.Close(ctx)
}

func runMigrations(dsn string) {
	absPath, _ := filepath.Abs("./migrations")

	// check migrations folder exists
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		slog.Warn("migrations folder does not exist, migrations will not be applied", "path", absPath)
		return
	}

	m, err := migrate.New("file://"+absPath, dsn)
	if err != nil {
		panic(err)
	}

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		panic(fmt.Errorf("migration up error: %w", err))
	}
}
