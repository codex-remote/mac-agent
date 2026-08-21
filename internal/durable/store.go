package durable

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ai-coding-remote/mac-agent/internal/protocol"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY,name TEXT NOT NULL,checksum TEXT NOT NULL,applied_at TEXT NOT NULL);
INSERT OR IGNORE INTO schema_migrations(version,name,checksum,applied_at) VALUES(1,'runtime durable run','runtime-agent-v1',strftime('%Y-%m-%dT%H:%M:%fZ','now'));
CREATE TABLE IF NOT EXISTS agent_runs(
 run_id TEXT PRIMARY KEY,start_command_id TEXT NOT NULL UNIQUE,start_payload_json TEXT NOT NULL,status TEXT NOT NULL,cancel_requested INTEGER NOT NULL DEFAULT 0 CHECK(cancel_requested IN(0,1)),
 codex_thread_id TEXT,codex_turn_id TEXT,next_agent_sequence INTEGER NOT NULL DEFAULT 1,durable_acked_sequence INTEGER NOT NULL DEFAULT 0,final_sequence INTEGER,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS result_outbox(
 run_id TEXT NOT NULL REFERENCES agent_runs(run_id),agent_sequence INTEGER NOT NULL,event_type TEXT NOT NULL,schema_version INTEGER NOT NULL DEFAULT 1,payload_json TEXT NOT NULL,occurred_at TEXT NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(run_id,agent_sequence));
CREATE TABLE IF NOT EXISTS bootstrap_syncs(
 sync_id TEXT PRIMARY KEY,command_id TEXT NOT NULL UNIQUE,snapshot_id TEXT NOT NULL,status TEXT NOT NULL,next_cursor TEXT,last_durable_batch_no INTEGER NOT NULL DEFAULT -1,last_batch_checksum TEXT,updated_at TEXT NOT NULL);`)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) PrepareRun(ctx context.Context, runID, commandID string, payload any) (bool, error) {
	data, _ := json.Marshal(payload)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO agent_runs(run_id,start_command_id,start_payload_json,status,created_at,updated_at) VALUES(?,?,?,'accepted',?,?)`, runID, commandID, string(data), now, now)
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	if affected == 1 {
		return true, nil
	}
	var storedCommand, storedPayload string
	if err := s.db.QueryRowContext(ctx, `SELECT start_command_id,start_payload_json FROM agent_runs WHERE run_id=?`, runID).Scan(&storedCommand, &storedPayload); err != nil {
		return false, err
	}
	if storedCommand != commandID || storedPayload != string(data) {
		return false, fmt.Errorf("run %s command conflict", runID)
	}
	return false, nil
}

func (s *Store) AppendEvent(ctx context.Context, runID string, message protocol.Message) (protocol.Message, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return message, err
	}
	defer tx.Rollback()
	var sequence int64
	if err := tx.QueryRowContext(ctx, `SELECT next_agent_sequence FROM agent_runs WHERE run_id=?`, runID).Scan(&sequence); err != nil {
		return message, err
	}
	message.AgentSequence = sequence
	occurred := message.OccurredAt.UTC().Format(time.RFC3339Nano)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO result_outbox(run_id,agent_sequence,event_type,payload_json,occurred_at,created_at) VALUES(?,?,?,?,?,?)`, runID, sequence, message.Type, string(message.Payload), occurred, now); err != nil {
		return message, err
	}
	terminal := message.Type == protocol.TypeTurnCompleted || message.Type == protocol.TypeTurnFailed || message.Type == protocol.TypeTurnInterrupted
	if terminal {
		if _, err := tx.ExecContext(ctx, `UPDATE agent_runs SET next_agent_sequence=?,final_sequence=?,status='terminal',updated_at=? WHERE run_id=?`, sequence+1, sequence, now, runID); err != nil {
			return message, err
		}
	} else if _, err := tx.ExecContext(ctx, `UPDATE agent_runs SET next_agent_sequence=?,updated_at=? WHERE run_id=?`, sequence+1, now, runID); err != nil {
		return message, err
	}
	return message, tx.Commit()
}

func (s *Store) Pending(ctx context.Context) ([]protocol.Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT o.run_id,o.agent_sequence,o.event_type,o.payload_json,o.occurred_at FROM result_outbox o JOIN agent_runs r ON r.run_id=o.run_id WHERE o.agent_sequence>r.durable_acked_sequence ORDER BY o.run_id,o.agent_sequence`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []protocol.Message
	for rows.Next() {
		var runID, eventType, payload, occurred string
		var sequence int64
		if err := rows.Scan(&runID, &sequence, &eventType, &payload, &occurred); err != nil {
			return nil, err
		}
		when, _ := time.Parse(time.RFC3339Nano, occurred)
		result = append(result, protocol.Message{SpecVersion: protocol.SpecVersion, MessageID: protocol.NewID(), Type: eventType, OccurredAt: when, TraceID: runID, Sender: protocol.Sender{Kind: "device", ID: "local-mac"}, Payload: json.RawMessage(payload), AgentSequence: sequence})
	}
	return result, rows.Err()
}

func (s *Store) DurableAck(ctx context.Context, runID string, sequence int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var next int64
	if err := tx.QueryRowContext(ctx, `SELECT next_agent_sequence FROM agent_runs WHERE run_id=?`, runID).Scan(&next); err != nil {
		return err
	}
	if sequence < 0 || sequence >= next {
		return fmt.Errorf("invalid durable ack %d for run %s", sequence, runID)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_runs SET durable_acked_sequence=MAX(durable_acked_sequence,?),updated_at=? WHERE run_id=?`, sequence, time.Now().UTC().Format(time.RFC3339Nano), runID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM result_outbox WHERE run_id=? AND agent_sequence<=?`, runID, sequence); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Cancel(ctx context.Context, runID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE agent_runs SET cancel_requested=1,updated_at=? WHERE run_id=?`, time.Now().UTC().Format(time.RFC3339Nano), runID)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return errors.New("run not found")
	}
	return nil
}

func (s *Store) PrepareBootstrap(ctx context.Context, syncID, commandID string) (string, int64, error) {
	snapshotID := protocol.NewID()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO bootstrap_syncs(sync_id,command_id,snapshot_id,status,updated_at) VALUES(?,?,?,'running',?)`, syncID, commandID, snapshotID, now)
	if err != nil {
		return "", -1, err
	}
	var storedCommand string
	var last int64
	if err := s.db.QueryRowContext(ctx, `SELECT command_id,snapshot_id,last_durable_batch_no FROM bootstrap_syncs WHERE sync_id=?`, syncID).Scan(&storedCommand, &snapshotID, &last); err != nil {
		return "", -1, err
	}
	if storedCommand != commandID {
		return "", -1, fmt.Errorf("bootstrap %s command conflict", syncID)
	}
	return snapshotID, last, nil
}

func (s *Store) AckBootstrap(ctx context.Context, syncID string, batchNo int64, checksum string, done bool) error {
	status := "running"
	if done {
		status = "completed"
	}
	result, err := s.db.ExecContext(ctx, `UPDATE bootstrap_syncs SET status=?,last_durable_batch_no=?,last_batch_checksum=?,updated_at=? WHERE sync_id=? AND last_durable_batch_no<?`, status, batchNo, checksum, time.Now().UTC().Format(time.RFC3339Nano), syncID, batchNo)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		var existingNo int64
		var existingChecksum string
		if err := s.db.QueryRowContext(ctx, `SELECT last_durable_batch_no,COALESCE(last_batch_checksum,'') FROM bootstrap_syncs WHERE sync_id=?`, syncID).Scan(&existingNo, &existingChecksum); err != nil {
			return err
		}
		if existingNo != batchNo || existingChecksum != checksum {
			return fmt.Errorf("bootstrap ack conflict")
		}
	}
	return nil
}
