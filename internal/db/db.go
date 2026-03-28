package db

import (
	"database/sql"
	"errors"
	"fmt"

	_ "github.com/mattn/go-sqlite3"

	"github.com/jonsampson/gopherclaw/internal/types"
)

// DB wraps a SQLite connection with gopherclaw-specific operations.
type DB struct {
	conn *sql.DB
}

// GroupCursor carries a per-group query context for GetNewMessages.
type GroupCursor struct {
	ChatJID        string
	SinceTimestamp int64
	LastBotMsgID   string
}

const schema = `
CREATE TABLE IF NOT EXISTS messages (
	id          TEXT NOT NULL,
	chat_jid    TEXT NOT NULL,
	sender      TEXT NOT NULL,
	content     TEXT NOT NULL,
	timestamp   INTEGER NOT NULL,
	is_from_me  INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (id, chat_jid)
);
CREATE INDEX IF NOT EXISTS idx_messages_chat_ts ON messages (chat_jid, timestamp);

CREATE TABLE IF NOT EXISTS chats (
	jid           TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	last_activity INTEGER NOT NULL DEFAULT 0,
	is_group      INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS registered_groups (
	jid     TEXT PRIMARY KEY,
	name    TEXT NOT NULL,
	folder  TEXT NOT NULL,
	trigger TEXT NOT NULL DEFAULT '',
	is_main INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS scheduled_tasks (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	group_folder   TEXT NOT NULL,
	prompt         TEXT NOT NULL,
	schedule_type  TEXT NOT NULL,
	schedule_value TEXT NOT NULL DEFAULT '',
	status         TEXT NOT NULL DEFAULT 'active',
	next_run       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_tasks_next_run ON scheduled_tasks (next_run, status);

CREATE TABLE IF NOT EXISTS task_run_logs (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id INTEGER NOT NULL REFERENCES scheduled_tasks(id) ON DELETE CASCADE,
	ran_at  INTEGER NOT NULL,
	status  TEXT NOT NULL,
	output  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS router_state (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	group_folder TEXT PRIMARY KEY,
	session_id   TEXT NOT NULL
);
`

func open(dataSource string) (*DB, error) {
	conn, err := sql.Open("sqlite3", dataSource)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		conn.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := conn.Exec(schema); err != nil {
		conn.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &DB{conn: conn}, nil
}

// InitDB opens (or creates) the database at the given file path.
func InitDB(path string) (*DB, error) {
	return open(path)
}

// InitTestDB opens an in-memory database suitable for unit tests.
func InitTestDB() (*DB, error) {
	return open(":memory:")
}

// Close releases the underlying database connection.
func (d *DB) Close() error {
	return d.conn.Close()
}

// StoreMessage persists a message, ignoring blank content. Upserts on (id, chat_jid).
func (d *DB) StoreMessage(msg types.NewMessage) error {
	if msg.Content == "" {
		return nil
	}
	_, err := d.conn.Exec(`
		INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id, chat_jid) DO UPDATE SET
			sender    = excluded.sender,
			content   = excluded.content,
			timestamp = excluded.timestamp,
			is_from_me = excluded.is_from_me
	`, msg.ID, msg.ChatJID, msg.Sender, msg.Content, msg.Timestamp, boolToInt(msg.IsFromMe))
	return err
}

// GetMessagesSince returns non-bot messages for a chat after sinceTimestamp.
// If sinceTimestamp==0 and lastBotMsgID is non-empty, the timestamp is recovered
// from the last bot message with that ID.  The result is capped to limit and
// returned in chronological order (oldest first).
func (d *DB) GetMessagesSince(chatJID string, sinceTimestamp int64, lastBotMsgID string, limit int) ([]types.NewMessage, error) {
	since := sinceTimestamp

	// Cursor recovery: if no explicit cursor, use the last bot reply timestamp.
	if since == 0 && lastBotMsgID != "" {
		var botTS int64
		err := d.conn.QueryRow(
			`SELECT timestamp FROM messages WHERE id=? AND chat_jid=? AND is_from_me=1`,
			lastBotMsgID, chatJID,
		).Scan(&botTS)
		if err == nil {
			since = botTS
		}
	}

	rows, err := d.conn.Query(`
		SELECT id, chat_jid, sender, content, timestamp, is_from_me
		FROM (
			SELECT id, chat_jid, sender, content, timestamp, is_from_me
			FROM messages
			WHERE chat_jid=? AND timestamp>? AND is_from_me=0
			ORDER BY timestamp DESC
			LIMIT ?
		)
		ORDER BY timestamp ASC
	`, chatJID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

// GetNewMessages aggregates messages across multiple groups using per-group cursors.
func (d *DB) GetNewMessages(groups []GroupCursor, limit int) ([]types.NewMessage, error) {
	var all []types.NewMessage
	for _, g := range groups {
		msgs, err := d.GetMessagesSince(g.ChatJID, g.SinceTimestamp, g.LastBotMsgID, limit)
		if err != nil {
			return nil, err
		}
		all = append(all, msgs...)
	}
	return all, nil
}

func scanMessages(rows *sql.Rows) ([]types.NewMessage, error) {
	var msgs []types.NewMessage
	for rows.Next() {
		var m types.NewMessage
		var fromMe int
		if err := rows.Scan(&m.ID, &m.ChatJID, &m.Sender, &m.Content, &m.Timestamp, &fromMe); err != nil {
			return nil, err
		}
		m.IsFromMe = fromMe != 0
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// StoreChatMetadata records chat info. Name defaults to JID if empty.
// The timestamp is preserved if the stored value is newer.
func (d *DB) StoreChatMetadata(jid, name string, isGroup bool, lastActivity int64) error {
	if name == "" {
		name = jid
	}
	_, err := d.conn.Exec(`
		INSERT INTO chats (jid, name, last_activity, is_group)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
			name          = excluded.name,
			last_activity = MAX(last_activity, excluded.last_activity),
			is_group      = excluded.is_group
	`, jid, name, lastActivity, boolToInt(isGroup))
	return err
}

// GetAllChats returns all known chats.
func (d *DB) GetAllChats() ([]types.ChatInfo, error) {
	rows, err := d.conn.Query(
		`SELECT jid, name, last_activity, is_group FROM chats ORDER BY last_activity DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var chats []types.ChatInfo
	for rows.Next() {
		var c types.ChatInfo
		var isGroup int
		if err := rows.Scan(&c.JID, &c.Name, &c.LastActivity, &isGroup); err != nil {
			return nil, err
		}
		c.IsGroup = isGroup != 0
		chats = append(chats, c)
	}
	return chats, rows.Err()
}

// CreateTask inserts a new scheduled task and returns its ID.
func (d *DB) CreateTask(task types.ScheduledTask) (int64, error) {
	res, err := d.conn.Exec(`
		INSERT INTO scheduled_tasks (group_folder, prompt, schedule_type, schedule_value, status, next_run)
		VALUES (?, ?, ?, ?, ?, ?)
	`, task.GroupFolder, task.Prompt, task.ScheduleType, task.ScheduleValue, task.Status, task.NextRun)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetTaskByID retrieves a task by its primary key.
func (d *DB) GetTaskByID(id int64) (*types.ScheduledTask, error) {
	row := d.conn.QueryRow(`
		SELECT id, group_folder, prompt, schedule_type, schedule_value, status, next_run
		FROM scheduled_tasks WHERE id=?
	`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task %d not found", id)
	}
	return t, err
}

// GetAllTasks returns every scheduled task.
func (d *DB) GetAllTasks() ([]types.ScheduledTask, error) {
	rows, err := d.conn.Query(`
		SELECT id, group_folder, prompt, schedule_type, schedule_value, status, next_run
		FROM scheduled_tasks
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTasks(rows)
}

// GetDueTasks returns active tasks whose next_run <= now (unix seconds).
func (d *DB) GetDueTasks(now int64) ([]types.ScheduledTask, error) {
	rows, err := d.conn.Query(`
		SELECT id, group_folder, prompt, schedule_type, schedule_value, status, next_run
		FROM scheduled_tasks
		WHERE status='active' AND next_run<=?
	`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTasks(rows)
}

// UpdateTask replaces the mutable fields of an existing task.
func (d *DB) UpdateTask(task types.ScheduledTask) error {
	_, err := d.conn.Exec(`
		UPDATE scheduled_tasks
		SET group_folder=?, prompt=?, schedule_type=?, schedule_value=?, status=?, next_run=?
		WHERE id=?
	`, task.GroupFolder, task.Prompt, task.ScheduleType, task.ScheduleValue, task.Status, task.NextRun, task.ID)
	return err
}

// DeleteTask removes a task and its run logs.
func (d *DB) DeleteTask(id int64) error {
	_, err := d.conn.Exec(`DELETE FROM scheduled_tasks WHERE id=?`, id)
	return err
}

// LogTaskRun records an execution result.
func (d *DB) LogTaskRun(log types.TaskRunLog) error {
	_, err := d.conn.Exec(`
		INSERT INTO task_run_logs (task_id, ran_at, status, output) VALUES (?, ?, ?, ?)
	`, log.TaskID, log.RanAt, log.Status, log.Output)
	return err
}

// GetRouterState retrieves a persisted key-value pair.
func (d *DB) GetRouterState(key string) (string, error) {
	var value string
	err := d.conn.QueryRow(`SELECT value FROM router_state WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// SetRouterState persists a key-value pair.
func (d *DB) SetRouterState(key, value string) error {
	_, err := d.conn.Exec(`
		INSERT INTO router_state (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value
	`, key, value)
	return err
}

// GetSession returns the Claude session ID for a group folder.
func (d *DB) GetSession(groupFolder string) (string, error) {
	var sid string
	err := d.conn.QueryRow(`SELECT session_id FROM sessions WHERE group_folder=?`, groupFolder).Scan(&sid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return sid, err
}

// SetSession persists the Claude session ID for a group folder.
func (d *DB) SetSession(groupFolder, sessionID string) error {
	_, err := d.conn.Exec(`
		INSERT INTO sessions (group_folder, session_id) VALUES (?, ?)
		ON CONFLICT(group_folder) DO UPDATE SET session_id=excluded.session_id
	`, groupFolder, sessionID)
	return err
}

// GetRegisteredGroup returns the group config for a JID.
func (d *DB) GetRegisteredGroup(jid string) (*types.RegisteredGroup, error) {
	var g types.RegisteredGroup
	var isMain int
	err := d.conn.QueryRow(`
		SELECT name, folder, trigger, is_main FROM registered_groups WHERE jid=?
	`, jid).Scan(&g.Name, &g.Folder, &g.Trigger, &isMain)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("group %q not found", jid)
	}
	if err != nil {
		return nil, err
	}
	g.IsMain = isMain != 0
	return &g, nil
}

// SetRegisteredGroup stores or replaces the group config for a JID.
func (d *DB) SetRegisteredGroup(jid string, g types.RegisteredGroup) error {
	_, err := d.conn.Exec(`
		INSERT INTO registered_groups (jid, name, folder, trigger, is_main)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
			name    = excluded.name,
			folder  = excluded.folder,
			trigger = excluded.trigger,
			is_main = excluded.is_main
	`, jid, g.Name, g.Folder, g.Trigger, boolToInt(g.IsMain))
	return err
}

// GetAllRegisteredGroups returns all registered groups.
func (d *DB) GetAllRegisteredGroups() ([]types.RegisteredGroup, error) {
	rows, err := d.conn.Query(`
		SELECT name, folder, trigger, is_main FROM registered_groups
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []types.RegisteredGroup
	for rows.Next() {
		var g types.RegisteredGroup
		var isMain int
		if err := rows.Scan(&g.Name, &g.Folder, &g.Trigger, &isMain); err != nil {
			return nil, err
		}
		g.IsMain = isMain != 0
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func scanTask(row *sql.Row) (*types.ScheduledTask, error) {
	var t types.ScheduledTask
	err := row.Scan(&t.ID, &t.GroupFolder, &t.Prompt, &t.ScheduleType, &t.ScheduleValue, &t.Status, &t.NextRun)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func scanTasks(rows *sql.Rows) ([]types.ScheduledTask, error) {
	var tasks []types.ScheduledTask
	for rows.Next() {
		var t types.ScheduledTask
		if err := rows.Scan(&t.ID, &t.GroupFolder, &t.Prompt, &t.ScheduleType, &t.ScheduleValue, &t.Status, &t.NextRun); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}
