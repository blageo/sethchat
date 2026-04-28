package database

import (
	"database/sql"
	"strings"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			db.Close()
		}
	}()
	err = db.Ping() // Verify connection
	if err != nil {
		return nil, err
	}

	//enable foreign keys
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	if err != nil {
		return nil, err
	}

	//create tables if they don't exist
	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS users (
		user_id INTEGER PRIMARY KEY,
		name TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS sessions (
		session_id TEXT PRIMARY KEY,
		user_id INTEGER,
		expires_at TEXT,
		FOREIGN KEY (user_id) REFERENCES users(user_id)
	);
	CREATE TABLE IF NOT EXISTS user_rooms (
		user_id   INTEGER NOT NULL,
		room_name TEXT    NOT NULL,
		PRIMARY KEY (user_id, room_name),
		FOREIGN KEY (user_id) REFERENCES users(user_id)
	);
	CREATE TABLE IF NOT EXISTS squad (
		id          INTEGER PRIMARY KEY CHECK (id = 1),
		name        TEXT NOT NULL DEFAULT 'My Squad',
		description TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS user_squad_roles (
		user_id INTEGER PRIMARY KEY,
		role    TEXT NOT NULL DEFAULT 'member',
		FOREIGN KEY (user_id) REFERENCES users(user_id)
	);
	CREATE TABLE IF NOT EXISTS messages (
		id         INTEGER PRIMARY KEY,
		room_name  TEXT    NOT NULL,
		sender     TEXT    NOT NULL,
		content    TEXT    NOT NULL DEFAULT '',
		media_url  TEXT    NOT NULL DEFAULT '',
		media_type TEXT    NOT NULL DEFAULT '',
		timestamp  TEXT    NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_messages_room ON messages (room_name, id);
	CREATE TABLE IF NOT EXISTS media (
		id           TEXT PRIMARY KEY,
		content_type TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS user_public_keys (
		user_id     INTEGER PRIMARY KEY,
		public_key  TEXT NOT NULL,
		uploaded_at TEXT NOT NULL,
		FOREIGN KEY (user_id) REFERENCES users(user_id)
	);
	CREATE TABLE IF NOT EXISTS room_keys (
		room_name         TEXT NOT NULL,
		user_id           INTEGER NOT NULL,
		encrypted_key     TEXT NOT NULL,
		key_iv            TEXT NOT NULL,
		sender_public_key TEXT NOT NULL,
		PRIMARY KEY (room_name, user_id),
		FOREIGN KEY (user_id) REFERENCES users(user_id)
	);
	CREATE TABLE IF NOT EXISTS rooms (
		room_name  TEXT PRIMARY KEY,
		created_by INTEGER NOT NULL,
		FOREIGN KEY (created_by) REFERENCES users(user_id)
	);
	`)

	if err != nil {
		return nil, err
	}

	// Idempotent migrations: add E2EE columns to messages if not present.
	for _, col := range []string{
		`ALTER TABLE messages ADD COLUMN iv TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE messages ADD COLUMN is_encrypted INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(col); err != nil && !isDuplicateColumnError(err) {
			return nil, err
		}
	}

	return db, nil
}

// isDuplicateColumnError reports whether err indicates an ALTER TABLE ADD COLUMN
// that failed because the column already exists (SQLite error text).
func isDuplicateColumnError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate column name") || strings.Contains(msg, "already exists")
}
