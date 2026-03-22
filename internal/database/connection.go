package database

import (
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"

	"github.com/magendooro/magento2-customer-graphql-go/internal/config"
)

const dsnParams = "parseTime=true&charset=utf8mb4&collation=utf8mb4_unicode_ci&loc=UTC"

func NewConnection(cfg config.DatabaseConfig) (*sql.DB, error) {
	var dsn string
	if cfg.Host == "localhost" && cfg.Socket != "" {
		dsn = fmt.Sprintf("%s:%s@unix(%s)/%s?%s",
			cfg.User, cfg.Password, cfg.Socket, cfg.Name, dsnParams,
		)
	} else if cfg.Host == "localhost" {
		dsn = fmt.Sprintf("%s:%s@unix(/tmp/mysql.sock)/%s?%s",
			cfg.User, cfg.Password, cfg.Name, dsnParams,
		)
	} else {
		dsn = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?%s",
			cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Name, dsnParams,
		)
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}
