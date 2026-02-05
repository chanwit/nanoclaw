// Package db provides SQLite database operations for NanoClaw.
package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/nanoclaw/go-nanoclaw/internal/logger"
	"github.com/nanoclaw/go-nanoclaw/internal/types"
)

var log = logger.WithComponent("db")

// DB wraps the SQLite database connection.
type DB struct {
	conn *sql.DB
}

// New creates a new database connection and initializes the schema.
func New(dbPath string) (*DB, error) {
	conn, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db := &DB{conn: conn}
	if err := db.initSchema(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	log.Info().Str("path", dbPath).Msg("Database initialized")
	return db, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// initSchema creates the database schema if it doesn't exist.
func (db *DB) initSchema() error {
	schema := `
		CREATE TABLE IF NOT EXISTS chats (
			jid TEXT PRIMARY KEY,
			name TEXT,
			last_message_time TEXT
		);

		CREATE TABLE IF NOT EXISTS messages (
			id TEXT,
			chat_jid TEXT,
			sender TEXT,
			sender_name TEXT,
			content TEXT,
			timestamp TEXT,
			is_from_me INTEGER,
			PRIMARY KEY (id, chat_jid),
			FOREIGN KEY (chat_jid) REFERENCES chats(jid)
		);

		CREATE INDEX IF NOT EXISTS idx_timestamp ON messages(timestamp);

		CREATE TABLE IF NOT EXISTS scheduled_tasks (
			id TEXT PRIMARY KEY,
			group_folder TEXT NOT NULL,
			chat_jid TEXT NOT NULL,
			prompt TEXT NOT NULL,
			schedule_type TEXT NOT NULL,
			schedule_value TEXT NOT NULL,
			context_mode TEXT DEFAULT 'isolated',
			next_run TEXT,
			last_run TEXT,
			last_result TEXT,
			status TEXT DEFAULT 'active',
			created_at TEXT NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_next_run ON scheduled_tasks(next_run);
		CREATE INDEX IF NOT EXISTS idx_status ON scheduled_tasks(status);

		CREATE TABLE IF NOT EXISTS task_run_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			run_at TEXT NOT NULL,
			duration_ms INTEGER NOT NULL,
			status TEXT NOT NULL,
			result TEXT,
			error TEXT,
			FOREIGN KEY (task_id) REFERENCES scheduled_tasks(id)
		);

		CREATE INDEX IF NOT EXISTS idx_task_run_logs ON task_run_logs(task_id, run_at);
	`

	_, err := db.conn.Exec(schema)
	return err
}

// StoreChatMetadata stores or updates chat metadata.
func (db *DB) StoreChatMetadata(jid string, timestamp time.Time, name string) error {
	ts := timestamp.Format(time.RFC3339)
	_, err := db.conn.Exec(`
		INSERT INTO chats (jid, name, last_message_time)
		VALUES (?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
			name = COALESCE(?, name),
			last_message_time = MAX(last_message_time, ?)
	`, jid, name, ts, name, ts)
	return err
}

// StoreMessage stores a message in the database.
func (db *DB) StoreMessage(msg types.MessageContext) error {
	ts := msg.Timestamp.Format(time.RFC3339)
	isFromMe := 0
	if msg.IsFromMe {
		isFromMe = 1
	}

	_, err := db.conn.Exec(`
		INSERT OR IGNORE INTO messages (id, chat_jid, sender, sender_name, content, timestamp, is_from_me)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, msg.ID, msg.ChatJID, msg.Sender, msg.SenderName, msg.Content, ts, isFromMe)
	return err
}

// GetNewMessages retrieves new messages from registered groups since the given timestamp.
func (db *DB) GetNewMessages(jids []string, since time.Time, botPrefix string) ([]types.NewMessage, error) {
	if len(jids) == 0 {
		return nil, nil
	}

	// Build placeholders for JIDs
	placeholders := make([]string, len(jids))
	args := make([]interface{}, len(jids)+2)
	for i, jid := range jids {
		placeholders[i] = "?"
		args[i] = jid
	}
	args[len(jids)] = since.Format(time.RFC3339)
	args[len(jids)+1] = botPrefix + "%"

	query := fmt.Sprintf(`
		SELECT id, chat_jid, sender, sender_name, content, timestamp
		FROM messages
		WHERE chat_jid IN (%s)
		  AND timestamp > ?
		  AND is_from_me = 0
		  AND content NOT LIKE ?
		ORDER BY timestamp ASC
	`, strings.Join(placeholders, ","))

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []types.NewMessage
	for rows.Next() {
		var msg types.NewMessage
		if err := rows.Scan(&msg.ID, &msg.ChatJID, &msg.Sender, &msg.SenderName, &msg.Content, &msg.Timestamp); err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}

	return messages, rows.Err()
}

// GetMessagesSince retrieves messages from a chat since the given timestamp for context.
func (db *DB) GetMessagesSince(jid string, since time.Time, botPrefix string, limit int) ([]types.MessageContext, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := db.conn.Query(`
		SELECT id, chat_jid, sender, sender_name, content, timestamp, is_from_me
		FROM messages
		WHERE chat_jid = ?
		  AND timestamp > ?
		  AND content NOT LIKE ?
		ORDER BY timestamp DESC
		LIMIT ?
	`, jid, since.Format(time.RFC3339), botPrefix+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []types.MessageContext
	for rows.Next() {
		var msg types.MessageContext
		var ts string
		var isFromMe int
		if err := rows.Scan(&msg.ID, &msg.ChatJID, &msg.Sender, &msg.SenderName, &msg.Content, &ts, &isFromMe); err != nil {
			return nil, err
		}
		msg.Timestamp, _ = time.Parse(time.RFC3339, ts)
		msg.IsFromMe = isFromMe == 1
		messages = append(messages, msg)
	}

	// Reverse to chronological order
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, rows.Err()
}

// GetAllChats retrieves all chat metadata.
func (db *DB) GetAllChats() ([]types.ChatMetadata, error) {
	rows, err := db.conn.Query(`
		SELECT jid, name, last_message_time
		FROM chats
		ORDER BY last_message_time DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chats []types.ChatMetadata
	for rows.Next() {
		var chat types.ChatMetadata
		var ts sql.NullString
		var name sql.NullString
		if err := rows.Scan(&chat.JID, &name, &ts); err != nil {
			return nil, err
		}
		if name.Valid {
			chat.Name = name.String
		}
		if ts.Valid {
			chat.LastMessageTime, _ = time.Parse(time.RFC3339, ts.String)
		}
		chats = append(chats, chat)
	}

	return chats, rows.Err()
}

// UpdateChatName updates the name of a chat.
func (db *DB) UpdateChatName(jid, name string) error {
	_, err := db.conn.Exec(`
		UPDATE chats SET name = ? WHERE jid = ?
	`, name, jid)
	return err
}

// CreateTask creates a new scheduled task.
func (db *DB) CreateTask(task *types.ScheduledTask) error {
	_, err := db.conn.Exec(`
		INSERT INTO scheduled_tasks (id, group_folder, chat_jid, prompt, schedule_type, schedule_value, context_mode, next_run, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, task.ID, task.GroupFolder, task.ChatJID, task.Prompt, task.ScheduleType, task.ScheduleValue, task.ContextMode, task.NextRun, task.Status, task.CreatedAt)
	return err
}

// GetTask retrieves a task by ID.
func (db *DB) GetTask(id string) (*types.ScheduledTask, error) {
	var task types.ScheduledTask
	var nextRun, lastRun, lastResult sql.NullString

	err := db.conn.QueryRow(`
		SELECT id, group_folder, chat_jid, prompt, schedule_type, schedule_value, context_mode, next_run, last_run, last_result, status, created_at
		FROM scheduled_tasks
		WHERE id = ?
	`, id).Scan(&task.ID, &task.GroupFolder, &task.ChatJID, &task.Prompt, &task.ScheduleType, &task.ScheduleValue, &task.ContextMode, &nextRun, &lastRun, &lastResult, &task.Status, &task.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if nextRun.Valid {
		task.NextRun = &nextRun.String
	}
	if lastRun.Valid {
		task.LastRun = &lastRun.String
	}
	if lastResult.Valid {
		task.LastResult = &lastResult.String
	}

	return &task, nil
}

// GetDueTasks retrieves all tasks that are due to run.
func (db *DB) GetDueTasks() ([]types.ScheduledTask, error) {
	now := time.Now().Format(time.RFC3339)

	rows, err := db.conn.Query(`
		SELECT id, group_folder, chat_jid, prompt, schedule_type, schedule_value, context_mode, next_run, last_run, last_result, status, created_at
		FROM scheduled_tasks
		WHERE status = 'active' AND next_run IS NOT NULL AND next_run <= ?
		ORDER BY next_run ASC
	`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []types.ScheduledTask
	for rows.Next() {
		var task types.ScheduledTask
		var nextRun, lastRun, lastResult sql.NullString

		if err := rows.Scan(&task.ID, &task.GroupFolder, &task.ChatJID, &task.Prompt, &task.ScheduleType, &task.ScheduleValue, &task.ContextMode, &nextRun, &lastRun, &lastResult, &task.Status, &task.CreatedAt); err != nil {
			return nil, err
		}

		if nextRun.Valid {
			task.NextRun = &nextRun.String
		}
		if lastRun.Valid {
			task.LastRun = &lastRun.String
		}
		if lastResult.Valid {
			task.LastResult = &lastResult.String
		}

		tasks = append(tasks, task)
	}

	return tasks, rows.Err()
}

// GetTasksByGroup retrieves all tasks for a specific group.
func (db *DB) GetTasksByGroup(groupFolder string) ([]types.ScheduledTask, error) {
	rows, err := db.conn.Query(`
		SELECT id, group_folder, chat_jid, prompt, schedule_type, schedule_value, context_mode, next_run, last_run, last_result, status, created_at
		FROM scheduled_tasks
		WHERE group_folder = ?
		ORDER BY created_at DESC
	`, groupFolder)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return db.scanTasks(rows)
}

// GetAllTasks retrieves all tasks.
func (db *DB) GetAllTasks() ([]types.ScheduledTask, error) {
	rows, err := db.conn.Query(`
		SELECT id, group_folder, chat_jid, prompt, schedule_type, schedule_value, context_mode, next_run, last_run, last_result, status, created_at
		FROM scheduled_tasks
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return db.scanTasks(rows)
}

func (db *DB) scanTasks(rows *sql.Rows) ([]types.ScheduledTask, error) {
	var tasks []types.ScheduledTask
	for rows.Next() {
		var task types.ScheduledTask
		var nextRun, lastRun, lastResult sql.NullString

		if err := rows.Scan(&task.ID, &task.GroupFolder, &task.ChatJID, &task.Prompt, &task.ScheduleType, &task.ScheduleValue, &task.ContextMode, &nextRun, &lastRun, &lastResult, &task.Status, &task.CreatedAt); err != nil {
			return nil, err
		}

		if nextRun.Valid {
			task.NextRun = &nextRun.String
		}
		if lastRun.Valid {
			task.LastRun = &lastRun.String
		}
		if lastResult.Valid {
			task.LastResult = &lastResult.String
		}

		tasks = append(tasks, task)
	}

	return tasks, rows.Err()
}

// UpdateTaskStatus updates the status of a task.
func (db *DB) UpdateTaskStatus(id string, status types.TaskStatus) error {
	_, err := db.conn.Exec(`
		UPDATE scheduled_tasks SET status = ? WHERE id = ?
	`, status, id)
	return err
}

// UpdateTaskAfterRun updates a task after execution.
func (db *DB) UpdateTaskAfterRun(id string, lastRun time.Time, lastResult string, nextRun *time.Time, status types.TaskStatus) error {
	var nextRunStr *string
	if nextRun != nil {
		s := nextRun.Format(time.RFC3339)
		nextRunStr = &s
	}

	// Truncate result if too long
	if len(lastResult) > 200 {
		lastResult = lastResult[:200] + "..."
	}

	_, err := db.conn.Exec(`
		UPDATE scheduled_tasks
		SET last_run = ?, last_result = ?, next_run = ?, status = ?
		WHERE id = ?
	`, lastRun.Format(time.RFC3339), lastResult, nextRunStr, status, id)
	return err
}

// DeleteTask deletes a task by ID.
func (db *DB) DeleteTask(id string) error {
	_, err := db.conn.Exec(`DELETE FROM scheduled_tasks WHERE id = ?`, id)
	return err
}

// LogTaskRun logs a task execution.
func (db *DB) LogTaskRun(log types.TaskRunLog) error {
	_, err := db.conn.Exec(`
		INSERT INTO task_run_logs (task_id, run_at, duration_ms, status, result, error)
		VALUES (?, ?, ?, ?, ?, ?)
	`, log.TaskID, log.RunAt, log.DurationMs, log.Status, log.Result, log.Error)
	return err
}

// GetTaskRunLogs retrieves run logs for a task.
func (db *DB) GetTaskRunLogs(taskID string, limit int) ([]types.TaskRunLog, error) {
	if limit <= 0 {
		limit = 10
	}

	rows, err := db.conn.Query(`
		SELECT id, task_id, run_at, duration_ms, status, result, error
		FROM task_run_logs
		WHERE task_id = ?
		ORDER BY run_at DESC
		LIMIT ?
	`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []types.TaskRunLog
	for rows.Next() {
		var log types.TaskRunLog
		var result, errMsg sql.NullString

		if err := rows.Scan(&log.ID, &log.TaskID, &log.RunAt, &log.DurationMs, &log.Status, &result, &errMsg); err != nil {
			return nil, err
		}

		if result.Valid {
			log.Result = &result.String
		}
		if errMsg.Valid {
			log.Error = &errMsg.String
		}

		logs = append(logs, log)
	}

	return logs, rows.Err()
}
