package database

import (
	"fmt"

	"github.com/carreira-cloud/ai-microservice/internal/config"
	"github.com/carreira-cloud/ai-microservice/internal/model"
	"github.com/glebarez/sqlite" // pure-Go SQLite (no CGO)
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Open creates and configures a DB connection.
// SQLite: uses AutoMigrate (dev/test only).
// Postgres: uses golang-migrate with SQL migration files.
func Open(cfg *config.Config) (*gorm.DB, error) {
	gormCfg := &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	}

	var db *gorm.DB
	var err error

	switch cfg.DBDriver {
	case "postgres":
		db, err = gorm.Open(postgres.Open(cfg.DBURL), gormCfg)
		if err != nil {
			return nil, fmt.Errorf("database: open postgres: %w", err)
		}
		if err := runMigrations(cfg.DBURL); err != nil {
			return nil, fmt.Errorf("database: run migrations: %w", err)
		}
	default:
		// SQLite — dev and test only
		db, err = gorm.Open(sqlite.Open(cfg.DBURL), gormCfg)
		if err != nil {
			return nil, fmt.Errorf("database: open sqlite: %w", err)
		}
		if err := db.AutoMigrate(
			&model.PromptTemplate{},
			&model.PromptVersion{},
			&model.AuditLog{},
		); err != nil {
			return nil, fmt.Errorf("database: automigrate: %w", err)
		}
	}
	return db, nil
}

// OpenTestDB opens a SQLite in-memory database for tests.
// Each call returns an isolated DB (no shared state between tests).
func OpenTestDB() (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open(":memory:?_foreign_keys=on"), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(
		&model.PromptTemplate{},
		&model.PromptVersion{},
		&model.AuditLog{},
	); err != nil {
		return nil, err
	}
	return db, nil
}
