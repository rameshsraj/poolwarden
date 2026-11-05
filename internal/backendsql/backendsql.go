// Package backendsql probes each PostgreSQL backend directly (design 01 §8):
// real role, replication state, slots, conflicts, long transactions and the
// connection budget. Queries use the extended protocol; passwords come from
// PGPASSFILE only.
package backendsql

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/smarsh/poolwarden/internal/model"
)

// Addr identifies one backend to probe.
type Addr struct {
	Host string
	Port int
	User string
	DB   string
}

// Prober returns a full Backend probe; failures are reported inside the
// returned Backend (Reachable/Err), never as a Go error, so one dead node
// never blocks the rest.
type Prober interface {
	Probe(ctx context.Context, a Addr) model.Backend
}

// The probe queries, exported so later milestones can reference them.
const (
	// QSelf works on primary and standby alike.
	// QSelf works on primary and standby alike. On a standby the control
	// file's timeline_id is the last restartpoint's TL, which lags after
	// rewind/follow — pg_stat_wal_receiver.received_tli is the live TL.
	QSelf = `SELECT pg_is_in_recovery(),
       current_setting('server_version_num')::int,
       CASE WHEN pg_is_in_recovery() AND EXISTS (SELECT 1 FROM pg_stat_wal_receiver)
            THEN (SELECT received_tli FROM pg_stat_wal_receiver LIMIT 1)
            ELSE (pg_control_checkpoint()).timeline_id END`
	QPrimaryLSN = `SELECT pg_current_wal_lsn()`
	QStandbyLSN = `SELECT pg_last_wal_receive_lsn(),
       pg_last_wal_replay_lsn(),
       pg_is_wal_replay_paused(),
       EXTRACT(EPOCH FROM now() - pg_last_xact_replay_timestamp())`
	QWalReceiver = `SELECT count(*) FROM pg_stat_wal_receiver`
	QReplication = `SELECT application_name, client_addr::text, state, sync_state,
       sent_lsn::text, write_lsn::text, flush_lsn::text, replay_lsn::text,
       pg_wal_lsn_diff(pg_current_wal_lsn(), replay_lsn),
       EXTRACT(EPOCH FROM write_lag),
       EXTRACT(EPOCH FROM flush_lag),
       EXTRACT(EPOCH FROM replay_lag)
FROM pg_stat_replication`
	QSlots = `SELECT slot_name, slot_type, active,
       COALESCE(wal_status, ''),
       COALESCE(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn), -1),
       COALESCE(database, '')
FROM pg_replication_slots`
	QLongTxns = `SELECT count(*),
       COALESCE(max(EXTRACT(EPOCH FROM now() - xact_start)), -1)
FROM pg_stat_activity
WHERE xact_start < now() - interval '5 minutes'
   OR (state = 'idle in transaction' AND state_change < now() - interval '5 minutes')`
	QConns = `SELECT current_setting('max_connections')::int,
       (SELECT count(*) FROM pg_stat_activity)`
	QConflicts = `SELECT COALESCE(sum(confl_tablespace + confl_lock + confl_snapshot
       + confl_bufferpin + confl_deadlock), 0)
FROM pg_stat_database_conflicts`
	QDBStats = `SELECT COALESCE(sum(deadlocks), 0), COALESCE(sum(xact_rollback), 0),
       COALESCE(sum(temp_files), 0)
FROM pg_stat_database`
	QClients = `SELECT COALESCE(datname, ''), COALESCE(usename, ''),
       COALESCE(application_name, ''), count(*)
FROM pg_stat_activity
WHERE backend_type = 'client backend'
GROUP BY 1, 2, 3`
)

// PGProber is the real Prober, backed by pgx with the default (extended)
// protocol.
type PGProber struct{}

func (PGProber) Probe(ctx context.Context, a Addr) model.Backend {
	b := model.Backend{Host: a.Host, Port: a.Port, NodeID: -1, ReplayDelaySec: -1}
	cfg, err := pgx.ParseConfig(fmt.Sprintf("host=%s port=%d user=%s dbname=%s application_name=poolwarden",
		a.Host, a.Port, a.User, a.DB))
	if err != nil {
		b.Err = err.Error()
		return b
	}
	if d, ok := ctx.Deadline(); ok {
		cfg.ConnectTimeout = time.Until(d)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		b.Err = err.Error()
		return b
	}
	defer conn.Close(ctx)
	b.Reachable = true

	var errs []string
	fail := func(what string, err error) {
		errs = append(errs, fmt.Sprintf("%s: %v", what, err))
	}

	var recovery bool
	var timeline int
	if err := conn.QueryRow(ctx, QSelf).Scan(&recovery, &b.Version, &timeline); err != nil {
		fail("self check", err)
		b.Err = strings.Join(errs, "; ")
		return b
	}
	b.InRecovery = recovery
	b.Timeline = timeline

	if b.InRecovery {
		var recv, replay *string
		var delay *float64
		if err := conn.QueryRow(ctx, QStandbyLSN).Scan(&recv, &replay, &b.ReplayPaused, &delay); err != nil {
			fail("standby lsn", err)
		} else {
			b.ReceiveLSN = deref(recv)
			b.CurrentLSN = deref(replay)
			if delay != nil {
				b.ReplayDelaySec = *delay
			}
		}
		var n int
		if err := conn.QueryRow(ctx, QWalReceiver).Scan(&n); err != nil {
			fail("wal receiver", err)
		} else {
			b.WALReceiver = n > 0
		}
		var conflicts int64
		if err := conn.QueryRow(ctx, QConflicts).Scan(&conflicts); err != nil {
			fail("conflicts", err)
		} else {
			b.Conflicts = conflicts
		}
	}
	// db stats apply to both roles
	if err := conn.QueryRow(ctx, QDBStats).Scan(&b.Deadlocks, &b.XactRollback, &b.TempFiles); err != nil {
		fail("db stats", err)
	}
	if !b.InRecovery {
		if err := conn.QueryRow(ctx, QPrimaryLSN).Scan(&b.CurrentLSN); err != nil {
			fail("current lsn", err)
		}
		standbys, err := queryReplication(ctx, conn)
		if err != nil {
			fail("replication", err)
		} else {
			b.Standbys = standbys
		}
	}
	slots, err := querySlots(ctx, conn)
	if err != nil {
		fail("slots", err)
	} else {
		b.Slots = slots
	}
	var oldest *float64
	if err := conn.QueryRow(ctx, QLongTxns).Scan(&b.LongTxns, &oldest); err != nil {
		fail("long txns", err)
	} else if oldest != nil {
		b.OldestXactSec = oldfloat(oldest)
	}
	if err := conn.QueryRow(ctx, QConns).Scan(&b.MaxConns, &b.Conns); err != nil {
		fail("connections", err)
	}
	clients, err := queryClients(ctx, conn)
	if err != nil {
		fail("client groups", err)
	} else {
		b.Clients = clients
	}
	b.Err = strings.Join(errs, "; ")
	return b
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func oldfloat(f *float64) float64 {
	if f == nil {
		return -1
	}
	return *f
}

// MapReplicationRow builds one StandbyRepl from a QReplication row's values.
func MapReplicationRow(app, addr, state, sync, sent, write, flush, replay string,
	lag int64, wlag, flag, rlag *float64) model.StandbyRepl {
	f := func(p *float64) float64 {
		if p == nil {
			return -1
		}
		return *p
	}
	return model.StandbyRepl{
		AppName: app, ClientAddr: addr, State: state, SyncState: sync,
		SentLSN: sent, WriteLSN: write, FlushLSN: flush, ReplayLSN: replay,
		LagBytes: lag, WriteLag: f(wlag), FlushLag: f(flag), ReplayLag: f(rlag),
	}
}

// MapSlotRow builds one Slot from a QSlots row's values.
func MapSlotRow(name, typ string, active bool, walStatus string, retained int64, db string) model.Slot {
	return model.Slot{Name: name, Type: typ, Active: active, WALStatus: walStatus,
		RetainedBytes: retained, Database: db}
}

func queryReplication(ctx context.Context, conn *pgx.Conn) ([]model.StandbyRepl, error) {
	rows, err := conn.Query(ctx, QReplication)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.StandbyRepl
	for rows.Next() {
		var app, addr, state, sync, sent, write, flush, replay string
		var lag int64
		var wl, fl, rl *float64
		if err := rows.Scan(&app, &addr, &state, &sync, &sent, &write, &flush, &replay,
			&lag, &wl, &fl, &rl); err != nil {
			return nil, err
		}
		out = append(out, MapReplicationRow(app, addr, state, sync, sent, write, flush,
			replay, lag, wl, fl, rl))
	}
	return out, rows.Err()
}

func querySlots(ctx context.Context, conn *pgx.Conn) ([]model.Slot, error) {
	rows, err := conn.Query(ctx, QSlots)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Slot
	for rows.Next() {
		var name, typ, wal, db string
		var active bool
		var retained int64
		if err := rows.Scan(&name, &typ, &active, &wal, &retained, &db); err != nil {
			return nil, err
		}
		out = append(out, MapSlotRow(name, typ, active, wal, retained, db))
	}
	return out, rows.Err()
}

func queryClients(ctx context.Context, conn *pgx.Conn) ([]model.ClientGroup, error) {
	rows, err := conn.Query(ctx, QClients)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ClientGroup
	for rows.Next() {
		var g model.ClientGroup
		if err := rows.Scan(&g.Database, &g.User, &g.Application, &g.Count); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
