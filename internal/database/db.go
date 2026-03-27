package database

import (
	"database/sql"

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
	`)

	if err != nil {
		return nil, err
	}
	return db, nil
}
