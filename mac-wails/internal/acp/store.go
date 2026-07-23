package acp

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Store persists sessions, their event log and idempotency keys in a SQLite
// database separate from the board's own DB.
type Store struct {
	db *sql.DB
}

// SessionRecord mirrors one agent_session row.
type SessionRecord struct {
	ID           string        `json:"id"`
	CardID       string        `json:"cardId"`
	BoardID      string        `json:"boardId"`
	AgentKind    string        `json:"agentKind"`
	ACPSessionID string        `json:"acpSessionId"`
	Status       SessionStatus `json:"status"`
	Cwd          string        `json:"cwd"`
	WorktreePath string        `json:"worktreePath"`
	Branch       string        `json:"branch"`
	StartedAt    time.Time     `json:"startedAt"`
	FinishedAt   *time.Time    `json:"finishedAt,omitempty"`
	ErrorText    string        `json:"errorText,omitempty"`
}

// SessionEventRecord mirrors one session_event row.
type SessionEventRecord struct {
	ID        int64           `json:"id"`
	SessionID string          `json:"sessionId"`
	Seq       int64           `json:"seq"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}

// OpenStore opens (creating if needed) the ACP database at path.
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS agent_session (
	id TEXT PRIMARY KEY,
	card_id TEXT NOT NULL,
	board_id TEXT NOT NULL,
	agent_kind TEXT NOT NULL,
	acp_session_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	cwd TEXT NOT NULL DEFAULT '',
	worktree_path TEXT NOT NULL DEFAULT '',
	branch TEXT NOT NULL DEFAULT '',
	started_at INTEGER NOT NULL,
	finished_at INTEGER,
	error_text TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_agent_session_card ON agent_session(card_id);
CREATE TABLE IF NOT EXISTS session_event (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL,
	seq INTEGER NOT NULL,
	kind TEXT NOT NULL,
	payload_json TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_session_event_session ON session_event(session_id, seq);
CREATE TABLE IF NOT EXISTS idempotency (
	key TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	created_at INTEGER NOT NULL
);`)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

// InsertSession stores a new session row.
func (s *Store) InsertSession(r SessionRecord) error {
	_, err := s.db.Exec(`INSERT INTO agent_session
		(id, card_id, board_id, agent_kind, acp_session_id, status, cwd, worktree_path, branch, started_at, error_text)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.CardID, r.BoardID, r.AgentKind, r.ACPSessionID, string(r.Status),
		r.Cwd, r.WorktreePath, r.Branch, r.StartedAt.UnixMilli(), r.ErrorText)
	return err
}

// UpdateSession updates the mutable fields of a session row.
func (s *Store) UpdateSession(id string, status SessionStatus, acpSessionID, cwd, worktreePath, branch, errorText string, finishedAt *time.Time) error {
	var fin any
	if finishedAt != nil {
		fin = finishedAt.UnixMilli()
	}
	_, err := s.db.Exec(`UPDATE agent_session
		SET status=?, acp_session_id=?, cwd=?, worktree_path=?, branch=?, error_text=?, finished_at=?
		WHERE id=?`,
		string(status), acpSessionID, cwd, worktreePath, branch, errorText, fin, id)
	return err
}

// SetSessionStatus updates only the status (and finished_at for terminal states).
func (s *Store) SetSessionStatus(id string, status SessionStatus, errorText string) error {
	var fin any
	if status.Terminal() {
		fin = time.Now().UnixMilli()
	}
	_, err := s.db.Exec(`UPDATE agent_session SET status=?, error_text=?, finished_at=COALESCE(?, finished_at) WHERE id=?`,
		string(status), errorText, fin, id)
	return err
}

// AppendEvent stores one session event.
func (s *Store) AppendEvent(sessionID string, seq int64, kind string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		b = []byte(fmt.Sprintf("%q", fmt.Sprint(payload)))
	}
	_, err = s.db.Exec(`INSERT INTO session_event (session_id, seq, kind, payload_json, created_at) VALUES (?,?,?,?,?)`,
		sessionID, seq, kind, string(b), time.Now().UnixMilli())
	return err
}

// SessionsForCard returns all sessions of a card, newest first, with events.
func (s *Store) SessionsForCard(cardID string) ([]SessionRecord, []SessionEventRecord, error) {
	rows, err := s.db.Query(`SELECT id, card_id, board_id, agent_kind, acp_session_id, status, cwd, worktree_path, branch, started_at, finished_at, error_text
		FROM agent_session WHERE card_id=? ORDER BY started_at DESC`, cardID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var sessions []SessionRecord
	for rows.Next() {
		r, err := scanSession(rows)
		if err != nil {
			return nil, nil, err
		}
		sessions = append(sessions, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var events []SessionEventRecord
	for _, sess := range sessions {
		evRows, err := s.db.Query(`SELECT id, session_id, seq, kind, payload_json, created_at
			FROM session_event WHERE session_id=? ORDER BY seq`, sess.ID)
		if err != nil {
			return nil, nil, err
		}
		for evRows.Next() {
			var ev SessionEventRecord
			var payload string
			var created int64
			if err := evRows.Scan(&ev.ID, &ev.SessionID, &ev.Seq, &ev.Kind, &payload, &created); err != nil {
				evRows.Close()
				return nil, nil, err
			}
			ev.Payload = json.RawMessage(payload)
			ev.CreatedAt = time.UnixMilli(created)
			events = append(events, ev)
		}
		evRows.Close()
		if err := evRows.Err(); err != nil {
			return nil, nil, err
		}
	}
	return sessions, events, nil
}

// StaleSessions returns sessions left in a non-terminal state (e.g. after an
// app crash/restart).
func (s *Store) StaleSessions() ([]SessionRecord, error) {
	rows, err := s.db.Query(`SELECT id, card_id, board_id, agent_kind, acp_session_id, status, cwd, worktree_path, branch, started_at, finished_at, error_text
		FROM agent_session WHERE status IN (?,?,?)`,
		string(StatusQueued), string(StatusRunning), string(StatusWaitingPermission))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionRecord
	for rows.Next() {
		r, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ClaimIdempotency records key and reports whether it was free (true = first
// claim within the window; false = duplicate). Expired keys are purged first.
func (s *Store) ClaimIdempotency(key, sessionID string, window time.Duration) (bool, error) {
	cutoff := time.Now().Add(-window).UnixMilli()
	if _, err := s.db.Exec(`DELETE FROM idempotency WHERE created_at < ?`, cutoff); err != nil {
		return false, err
	}
	res, err := s.db.Exec(`INSERT OR IGNORE INTO idempotency (key, session_id, created_at) VALUES (?,?,?)`,
		key, sessionID, time.Now().UnixMilli())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func scanSession(rows *sql.Rows) (SessionRecord, error) {
	var r SessionRecord
	var status string
	var started int64
	var finished sql.NullInt64
	if err := rows.Scan(&r.ID, &r.CardID, &r.BoardID, &r.AgentKind, &r.ACPSessionID, &status,
		&r.Cwd, &r.WorktreePath, &r.Branch, &started, &finished, &r.ErrorText); err != nil {
		return r, err
	}
	r.Status = SessionStatus(status)
	r.StartedAt = time.UnixMilli(started)
	if finished.Valid {
		t := time.UnixMilli(finished.Int64)
		r.FinishedAt = &t
	}
	return r, nil
}
