// Package migrate runs embedded goose migrations against a Postgres
// database. Migration SQL lives in migrations/ and is embedded at build
// time so services ship without a separate migrations directory on disk.
package migrate

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// setDialect is goose.SetDialect by default; tests override it to exercise
// the error path that "postgres" (the only dialect this package ever
// passes) cannot otherwise reach.
var setDialect = goose.SetDialect

// Up applies all pending migrations to db.
func Up(db *sql.DB) error {
	goose.SetBaseFS(migrations)
	defer goose.SetBaseFS(nil)

	if err := setDialect("postgres"); err != nil {
		return fmt.Errorf("migrate: set dialect: %w", err)
	}

	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("migrate: up: %w", err)
	}

	return nil
}

// Status reports the current migration version applied to db.
func Status(db *sql.DB) (int64, error) {
	goose.SetBaseFS(migrations)
	defer goose.SetBaseFS(nil)

	if err := setDialect("postgres"); err != nil {
		return 0, fmt.Errorf("migrate: set dialect: %w", err)
	}

	version, err := goose.GetDBVersion(db)
	if err != nil {
		return 0, fmt.Errorf("migrate: get version: %w", err)
	}

	return version, nil
}
