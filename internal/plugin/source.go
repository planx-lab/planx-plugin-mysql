// Package plugin implements the MySQL source plugin logic.
// This file implements only the business logic (SPI interface).
// gRPC handling, session management, and flow control are handled by SDK.
package plugin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/planx-lab/planx-common/logger"
	"github.com/planx-lab/planx-sdk-go/batch"
	"github.com/planx-lab/planx-sdk-go/source"
)

// Config holds the MySQL source configuration.
type Config struct {
	DSN          string `json:"dsn"`
	Table        string `json:"table"`
	Query        string `json:"query"`
	BatchSize    int    `json:"batch_size"`
	PollInterval string `json:"poll_interval"`
}

// MySQLSourceSPI implements source.SPI interface.
// Only business logic - no gRPC handling.
type MySQLSourceSPI struct {
	db     *sql.DB
	cfg    Config
	offset int
	ticker *time.Ticker
}

// NewMySQLSourceFactory returns a factory function for SDK server.
func NewMySQLSourceFactory() source.Factory {
	return func() source.SPI {
		return &MySQLSourceSPI{}
	}
}

// Init implements source.SPI - connects to MySQL.
func (s *MySQLSourceSPI) Init(ctx context.Context, config []byte) error {
	if err := json.Unmarshal(config, &s.cfg); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	if s.cfg.DSN == "" {
		return fmt.Errorf("dsn is required")
	}

	if s.cfg.BatchSize <= 0 {
		s.cfg.BatchSize = 100
	}

	// Connect to MySQL
	db, err := sql.Open("mysql", s.cfg.DSN)
	if err != nil {
		return fmt.Errorf("failed to connect to MySQL: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return fmt.Errorf("failed to ping MySQL: %w", err)
	}

	s.db = db

	// Setup polling
	interval := 5 * time.Second
	if s.cfg.PollInterval != "" {
		if d, err := time.ParseDuration(s.cfg.PollInterval); err == nil {
			interval = d
		}
	}
	s.ticker = time.NewTicker(interval)

	logger.Info().Str("table", s.cfg.Table).Msg("MySQL source initialized")
	return nil
}

// ReadBatch implements source.SPI - reads next batch from MySQL.
func (s *MySQLSourceSPI) ReadBatch(ctx context.Context) (batch.Batch, error) {
	// Wait for next poll interval
	select {
	case <-ctx.Done():
		return batch.Batch{}, io.EOF
	case <-s.ticker.C:
	}

	// Build query
	query := s.cfg.Query
	if query == "" {
		query = fmt.Sprintf("SELECT * FROM %s", s.cfg.Table)
	}
	paginatedQuery := fmt.Sprintf("%s LIMIT %d OFFSET %d", query, s.cfg.BatchSize, s.offset)

	rows, err := s.db.QueryContext(ctx, paginatedQuery)
	if err != nil {
		return batch.Batch{}, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	records, err := rowsToRecords(rows, s.cfg.Table)
	if err != nil {
		return batch.Batch{}, err
	}

	if len(records) == 0 {
		s.offset = 0 // Reset for next poll
		return batch.Batch{}, nil
	}

	s.offset += len(records)

	logger.Debug().Int("records", len(records)).Int("offset", s.offset).Msg("MySQL batch read")

	return batch.Batch{
		Records: records,
		Context: map[string]string{
			"table": s.cfg.Table,
		},
	}, nil
}

// Close implements source.SPI - closes MySQL connection.
func (s *MySQLSourceSPI) Close() error {
	if s.ticker != nil {
		s.ticker.Stop()
	}
	if s.db != nil {
		s.db.Close()
	}
	logger.Info().Msg("MySQL source closed")
	return nil
}

// rowsToRecords converts SQL rows to batch records.
func rowsToRecords(rows *sql.Rows, table string) ([]batch.Record, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var records []batch.Record
	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, err
		}

		rowMap := make(map[string]interface{})
		for i, col := range columns {
			val := values[i]
			if b, ok := val.([]byte); ok {
				rowMap[col] = string(b)
			} else {
				rowMap[col] = val
			}
		}

		payload, err := json.Marshal(rowMap)
		if err != nil {
			return nil, err
		}

		records = append(records, batch.Record{
			Metadata: map[string]string{
				"source": "mysql",
				"table":  table,
			},
			Payload: payload,
		})
	}

	return records, rows.Err()
}
