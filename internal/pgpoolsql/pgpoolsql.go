// Package pgpoolsql is the adapter for pgpool-II's SQL interface: the
// SHOW POOL_* commands and PGPOOL SHOW, over the simple query protocol.
// Passwords come from PGPASSFILE only; this package never sets one.
package pgpoolsql

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/model"
)

// Row is one result row keyed by column name (columns differ between pgpool
// versions, so everything parses by name, never by position).
type Row map[string]string

// Client is a connection to pgpool's SQL port.
type Client interface {
	Version(ctx context.Context) (string, error)
	PoolNodes(ctx context.Context) ([]model.PoolNode, error)
	PoolProcesses(ctx context.Context) ([]model.PoolProcess, error)
	PoolPools(ctx context.Context) ([]model.PoolPool, error)
	HealthCheckStats(ctx context.Context) ([]model.HealthStat, error)
	BackendStats(ctx context.Context) ([]model.BackendStat, error)
	Show(ctx context.Context, param string) (string, error)
	ShowAll(ctx context.Context) (map[string]string, error)
	Close(ctx context.Context) error
}

// Dialer opens pgpool SQL connections; fakeable in tests.
type Dialer interface {
	Dial(ctx context.Context, ep config.Endpoint) (Client, error)
}

// PGDialer is the real Dialer, backed by pgx.
type PGDialer struct{}

func (PGDialer) Dial(ctx context.Context, ep config.Endpoint) (Client, error) {
	cfg, err := pgx.ParseConfig(fmt.Sprintf("host=%s port=%d user=%s dbname=%s application_name=poolwarden",
		ep.Host, ep.Port, ep.User, ep.DBName))
	if err != nil {
		return nil, err
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	if d, ok := ctx.Deadline(); ok {
		cfg.ConnectTimeout = time.Until(d)
	}
	if cfg.ConnectTimeout <= 0 {
		return nil, context.DeadlineExceeded
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &pgClient{conn: conn}, nil
}

type pgClient struct {
	conn *pgx.Conn
}

func (c *pgClient) Close(ctx context.Context) error { return c.conn.Close(ctx) }

func (c *pgClient) query(ctx context.Context, sql string) ([]Row, error) {
	rows, err := c.conn.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fields := rows.FieldDescriptions()
	var out []Row
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		r := Row{}
		for i, f := range fields {
			switch v := vals[i].(type) {
			case nil:
				r[f.Name] = ""
			case string:
				r[f.Name] = v
			default:
				r[f.Name] = fmt.Sprintf("%v", v)
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (c *pgClient) Version(ctx context.Context) (string, error) {
	rows, err := c.query(ctx, "SHOW POOL_VERSION")
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("SHOW POOL_VERSION returned no rows")
	}
	for _, v := range rows[0] {
		return v, nil
	}
	return "", fmt.Errorf("SHOW POOL_VERSION returned no columns")
}

func (c *pgClient) PoolNodes(ctx context.Context) ([]model.PoolNode, error) {
	rows, err := c.query(ctx, "SHOW POOL_NODES")
	if err != nil {
		return nil, err
	}
	return ParsePoolNodes(rows)
}

func (c *pgClient) PoolProcesses(ctx context.Context) ([]model.PoolProcess, error) {
	rows, err := c.query(ctx, "SHOW POOL_PROCESSES")
	if err != nil {
		return nil, err
	}
	return ParsePoolProcesses(rows)
}

func (c *pgClient) PoolPools(ctx context.Context) ([]model.PoolPool, error) {
	rows, err := c.query(ctx, "SHOW POOL_POOLS")
	if err != nil {
		return nil, err
	}
	return ParsePoolPools(rows)
}

func (c *pgClient) HealthCheckStats(ctx context.Context) ([]model.HealthStat, error) {
	rows, err := c.query(ctx, "SHOW POOL_HEALTH_CHECK_STATS")
	if err != nil {
		return nil, err
	}
	return ParseHealthCheckStats(rows)
}

func (c *pgClient) BackendStats(ctx context.Context) ([]model.BackendStat, error) {
	rows, err := c.query(ctx, "SHOW POOL_BACKEND_STATS")
	if err != nil {
		return nil, err
	}
	return ParseBackendStats(rows)
}

func (c *pgClient) Show(ctx context.Context, param string) (string, error) {
	rows, err := c.query(ctx, "PGPOOL SHOW "+param)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("PGPOOL SHOW %s: no rows", param)
	}
	return showValue(rows[0]), nil
}

func (c *pgClient) ShowAll(ctx context.Context) (map[string]string, error) {
	rows, err := c.query(ctx, "PGPOOL SHOW ALL")
	if err != nil {
		return nil, err
	}
	return ParseShowAll(rows), nil
}

// showValue picks the value column of a PGPOOL SHOW row: "value"/"setting"
// if present, else the second column.
func showValue(r Row) string {
	for _, k := range []string{"value", "setting"} {
		if v, ok := r[k]; ok {
			return v
		}
	}
	var keys []string
	for k := range r {
		keys = append(keys, k)
	}
	if len(keys) >= 2 {
		if keys[0] == "item" || keys[0] == "name" {
			return r[keys[1]]
		}
		return r[keys[0]]
	}
	if len(keys) == 1 {
		return r[keys[0]]
	}
	return ""
}

// ParseShowAll maps PGPOOL SHOW ALL rows (item/value or name/setting).
func ParseShowAll(rows []Row) map[string]string {
	out := map[string]string{}
	nameKey, valKey := "item", "value"
	for _, r := range rows {
		if _, ok := r["name"]; ok {
			nameKey, valKey = "name", "setting"
		}
		out[r[nameKey]] = r[valKey]
	}
	return out
}

var _ = strings.TrimSpace
