package auth

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var ErrSessionExpired = errors.New("session expired")

func Register(db *sql.DB, username, password string) (int64, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}
	result, err := db.Exec("INSERT INTO users (name, password_hash) VALUES (?, ?)", username, hash)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func Login(db *sql.DB, username, password string) (int64, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	var userID int64
	var hash []byte
	err := db.QueryRow("SELECT user_id, password_hash FROM users WHERE name = ?", username).Scan(&userID, &hash)
	if err != nil {
		return 0, err
	}
	err = bcrypt.CompareHashAndPassword(hash, []byte(password))
	if err != nil {
		return 0, err
	}
	return userID, nil
}

func CreateSession(db *sql.DB, userID int64) (string, error) {
	sessionID := uuid.New().String()
	expiresAt := time.Now().Add(7 * 24 * time.Hour).Format(time.RFC3339)
	_, err := db.Exec("INSERT INTO sessions (session_id, user_id, expires_at) VALUES (?, ?, ?)", sessionID, userID, expiresAt)
	if err != nil {
		return "", err
	}
	return sessionID, nil
}

func ValidateSession(db *sql.DB, sessionID string) (int64, error) {
	var userID int64
	var expiresAtStr string
	err := db.QueryRow("SELECT user_id, expires_at FROM sessions WHERE session_id = ?", sessionID).Scan(&userID, &expiresAtStr)
	if err != nil {
		return 0, err
	}
	expiresAt, err := time.Parse(time.RFC3339, expiresAtStr)
	if err != nil {
		return 0, err
	}
	if time.Now().After(expiresAt) {
		return 0, ErrSessionExpired
	}
	return userID, nil
}
