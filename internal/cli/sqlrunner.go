package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"

	"github.com/smarsh/poolwarden/internal/service"
)

// pgxRunner is the real service.SQLRunner.
type pgxRunner struct{}

func (r pgxRunner) conn(ctx context.Context, addr string, port int) (*pgx.Conn, error) {
	return pgx.Connect(ctx,
		fmt.Sprintf("host=%s port=%d application_name=poolwarden", addr, port))
}

func (r pgxRunner) Exec(ctx context.Context, addr string, port int, sql string) error {
	c, err := r.conn(ctx, addr, port)
	if err != nil {
		return err
	}
	defer c.Close(ctx)
	_, err = c.Exec(ctx, sql)
	return err
}

func (r pgxRunner) Query(ctx context.Context, addr string, port int, sql string) ([]string, [][]string, error) {
	c, err := r.conn(ctx, addr, port)
	if err != nil {
		return nil, nil, err
	}
	defer c.Close(ctx)
	rows, err := c.Query(ctx, sql)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var cols []string
	for _, fd := range rows.FieldDescriptions() {
		cols = append(cols, string(fd.Name))
	}
	var out [][]string
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, nil, err
		}
		var row []string
		for _, v := range vals {
			row = append(row, fmt.Sprint(v))
		}
		out = append(out, row)
	}
	return cols, out, rows.Err()
}

// exportPassfiles sets PGPASSFILE/PCPPASSFILE from config (I28).
func exportPassfiles(a *app) {
	if a.cfg == nil {
		return
	}
	if p := a.cfg.Pgpool.Passfile; p != "" {
		os.Setenv("PGPASSFILE", p)
	}
	if p := a.cfg.PCP.Passfile; p != "" {
		os.Setenv("PCPPASSFILE", p)
	}
}

var _ = service.SQLRunner(pgxRunner{})
