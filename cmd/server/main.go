package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"sethchat/internal/auth"
	"sethchat/internal/database"
	"sethchat/internal/protocol"

	"github.com/gorilla/websocket"
)

// upgrader promotes HTTP connections to WebSocket. CheckOrigin is permissive
// since sethchat is intended for trusted private networks, not the public web.
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// db is the shared SQLite connection used by all HTTP and WebSocket handlers.
var db *sql.DB

// mediaDir is the directory where uploaded media files are stored on disk.
var mediaDir string

// Hub manages all active WebSocket connections, organized by room.
// clients maps room name → (connection → username).
type Hub struct {
	clients map[string]map[*websocket.Conn]string // room -> conn -> username
	mu      sync.RWMutex
}

// addToRoom registers conn as username in room, creating the room map if needed.
func (h *Hub) addToRoom(conn *websocket.Conn, user, room string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[room] == nil {
		h.clients[room] = make(map[*websocket.Conn]string)
	}
	h.clients[room][conn] = user
}

// removeFromRoom removes conn from a specific room only.
func (h *Hub) removeFromRoom(conn *websocket.Conn, room string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients[room], conn)
}

// roomsForConn returns all rooms the given connection is currently in.
// Caller must not hold h.mu.
func (h *Hub) roomsForConn(conn *websocket.Conn) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var rooms []string
	for room, conns := range h.clients {
		if _, ok := conns[conn]; ok {
			rooms = append(rooms, room)
		}
	}
	return rooms
}

// removeFromAll removes conn from every room. Called on disconnect to ensure
// no stale connections remain in the hub.
func (h *Hub) removeFromAll(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for room := range h.clients {
		delete(h.clients[room], conn)
	}
}

// broadcastToRoom sends message to every connection currently in room.
// Write errors are logged but do not abort the broadcast to other clients.
func (h *Hub) broadcastToRoom(message protocol.Message, sender *websocket.Conn, room string) {
	mes, err := json.Marshal(message)
	if err != nil {
		log.Println("marshal error:", err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	log.Printf("broadcasting to room %s, %d clients", room, len(h.clients[room]))
	for conn := range h.clients[room] {
		if err := conn.WriteMessage(websocket.TextMessage, mes); err != nil {
			log.Println("write error:", err)
		}
	}
}

// stamp sets the message timestamp to now if one hasn't been set already.
// The server is the authority on time; client-supplied timestamps are ignored.
func stamp(m protocol.Message) protocol.Message {
	if m.Timestamp.IsZero() {
		m.Timestamp = time.Now().UTC()
	}
	return m
}

var hub = Hub{clients: make(map[string]map[*websocket.Conn]string)}

const maxUploadSize = 50 << 20 // 50 MB

// allowedMediaTypes lists MIME prefixes accepted for upload.
var allowedMediaTypes = []string{"image/", "video/"}

// validateSessionParam reads ?session= from the request, validates it, and
// returns the resolved userID. Writes an HTTP error and returns (0, false) on failure.
func validateSessionParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	sid := r.URL.Query().Get("session")
	if sid == "" {
		http.Error(w, "session required", http.StatusUnauthorized)
		return 0, false
	}
	userID, err := auth.ValidateSession(db, sid)
	if err != nil {
		if errors.Is(err, auth.ErrSessionExpired) {
			http.Error(w, "session expired", http.StatusUnauthorized)
		} else {
			http.Error(w, "invalid session", http.StatusUnauthorized)
		}
		return 0, false
	}
	return userID, true
}

// lookupUsername resolves a userID (returned by auth.ValidateSession) to a
// username string by querying the users table directly.
func lookupUsername(userID int64) (string, error) {
	var name string
	err := db.QueryRow("SELECT name FROM users WHERE user_id = ?", userID).Scan(&name)
	return name, err
}

// handleUpload accepts a multipart file upload from an authenticated user,
// saves it to mediaDir on disk, and records its content type in the DB.
// Returns JSON: {"url":"/media/<id>","type":"image/jpeg"}.
// Accepted types: image/* and video/*. Max size: 50 MB.
func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := validateSessionParam(w, r); !ok {
		return
	}
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		http.Error(w, "file too large or malformed", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Detect content type from the first 512 bytes.
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	contentType := http.DetectContentType(buf[:n])

	allowed := false
	for _, prefix := range allowedMediaTypes {
		if strings.HasPrefix(contentType, prefix) {
			allowed = true
			break
		}
	}
	if !allowed {
		http.Error(w, "unsupported media type: "+contentType, http.StatusUnsupportedMediaType)
		return
	}

	id := fmt.Sprintf("%d", time.Now().UnixNano())
	dst, err := os.Create(filepath.Join(mediaDir, id))
	if err != nil {
		log.Printf("handleUpload: create file: %v", err)
		http.Error(w, "could not save file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	// Write the already-read bytes, then copy the rest.
	if _, err := dst.Write(buf[:n]); err != nil {
		http.Error(w, "could not save file", http.StatusInternalServerError)
		return
	}
	if _, err := dst.ReadFrom(file); err != nil {
		http.Error(w, "could not save file", http.StatusInternalServerError)
		return
	}

	if _, err := db.Exec("INSERT INTO media (id, content_type) VALUES (?, ?)", id, contentType); err != nil {
		log.Printf("handleUpload: db insert: %v", err)
		http.Error(w, "could not record upload", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"url":  "/media/" + id,
		"type": contentType,
	})
}

// handleMedia serves a previously uploaded media item from disk.
func handleMedia(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/media/")
	if id == "" || strings.ContainsAny(id, "/\\") {
		http.NotFound(w, r)
		return
	}
	var contentType string
	if err := db.QueryRow("SELECT content_type FROM media WHERE id = ?", id).Scan(&contentType); err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, id, time.Time{}, mustOpen(filepath.Join(mediaDir, id)))
}

// mustOpen opens a file for http.ServeContent. Returns nil on error, which
// causes ServeContent to respond with a 500 — acceptable for an internal helper.
func mustOpen(path string) *os.File {
	f, _ := os.Open(path)
	return f
}

// getUserRole returns the squad role for userID: "owner", "admin", or "member".
// Returns "member" if no role row exists (defensive default).
func getUserRole(userID int64) string {
	var role string
	if err := db.QueryRow("SELECT role FROM user_squad_roles WHERE user_id = ?", userID).Scan(&role); err != nil {
		return "member"
	}
	return role
}

// handleRegister creates a new user account.
// Expects POST with JSON body: {"username": "...", "password": "..."}.
// Returns 201 on success; the client must then call /login to get a session.
// The first user to register is automatically assigned the "owner" role.
func handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	userID, err := auth.Register(db, req.Username, req.Password)
	if err != nil {
		http.Error(w, "registration failed: "+err.Error(), http.StatusConflict)
		return
	}

	// Assign role: owner if no owner exists yet, otherwise member.
	role := "member"
	var ownerCount int
	db.QueryRow("SELECT COUNT(*) FROM user_squad_roles WHERE role = 'owner'").Scan(&ownerCount)
	if ownerCount == 0 {
		role = "owner"
	}
	db.Exec("INSERT INTO user_squad_roles (user_id, role) VALUES (?, ?)", userID, role)

	w.WriteHeader(http.StatusCreated)
}

// handleSquad returns squad info (GET) or updates it (PATCH, owner only).
//
//	GET  → {"name":"…","description":"…","your_role":"owner"}
//	PATCH body {"name":"…","description":"…"} → 204
func handleSquad(w http.ResponseWriter, r *http.Request) {
	userID, ok := validateSessionParam(w, r)
	if !ok {
		return
	}
	role := getUserRole(userID)

	switch r.Method {
	case http.MethodGet:
		var name, description string
		db.QueryRow("SELECT name, description FROM squad WHERE id = 1").Scan(&name, &description)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"name":        name,
			"description": description,
			"your_role":   role,
		})

	case http.MethodPatch:
		if role != "owner" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		db.Exec("UPDATE squad SET name = ?, description = ? WHERE id = 1", req.Name, req.Description)
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSquadMembers lists all members (GET) or changes a member's role (PATCH, owner only).
//
//	GET  → {"members":[{"id":1,"name":"alice","role":"owner"},…]}
//	PATCH body {"user_id":2,"role":"admin"} → 204
func handleSquadMembers(w http.ResponseWriter, r *http.Request) {
	userID, ok := validateSessionParam(w, r)
	if !ok {
		return
	}
	role := getUserRole(userID)

	switch r.Method {
	case http.MethodGet:
		rows, err := db.Query(`
			SELECT u.user_id, u.name, COALESCE(r.role, 'member')
			FROM users u
			LEFT JOIN user_squad_roles r ON u.user_id = r.user_id
			ORDER BY CASE COALESCE(r.role,'member')
				WHEN 'owner'  THEN 0
				WHEN 'admin'  THEN 1
				ELSE               2
			END, u.name ASC`)
		if err != nil {
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		type member struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Role string `json:"role"`
		}
		members := []member{}
		for rows.Next() {
			var m member
			rows.Scan(&m.ID, &m.Name, &m.Role)
			members = append(members, m)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"members": members})

	case http.MethodPatch:
		if role != "owner" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req struct {
			UserID int64  `json:"user_id"`
			Role   string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// Prevent owner from demoting themselves.
		if req.UserID == userID {
			http.Error(w, "cannot change your own role", http.StatusBadRequest)
			return
		}
		// Only 'admin' and 'member' are valid targets (can't promote to owner).
		if req.Role != "admin" && req.Role != "member" {
			http.Error(w, "invalid role", http.StatusBadRequest)
			return
		}
		db.Exec("UPDATE user_squad_roles SET role = ? WHERE user_id = ?", req.Role, req.UserID)
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleLogin authenticates a user and issues a session token.
// Expects POST with JSON body: {"username": "...", "password": "..."}.
// Returns JSON: {"session_id": "<uuid>"} on success.
// The session_id must be passed as the ?session= query param when opening /ws.
func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	userID, err := auth.Login(db, req.Username, req.Password)
	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	sessionID, err := auth.CreateSession(db, userID)
	if err != nil {
		http.Error(w, "could not create session", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"session_id": sessionID})
}

// saveMessage persists a chat message to the messages table.
// Errors are logged but do not affect the caller — a failed write is non-fatal.
func saveMessage(m protocol.Message) {
	_, err := db.Exec(
		`INSERT INTO messages (room_name, sender, content, media_url, media_type, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		m.Room, m.Sender, m.Content, m.MediaURL, m.MediaType,
		m.Timestamp.UTC().Format(time.RFC3339),
	)
	if err != nil {
		log.Printf("saveMessage error: %v", err)
	}
}

// handleHistory returns the most recent chat messages for a room.
//
//	GET /history?room=<name>&session=<sid>
//	→ {"messages": [...]}  (chronological order, up to 50 messages)
func handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := validateSessionParam(w, r); !ok {
		return
	}
	room := r.URL.Query().Get("room")
	if room == "" {
		http.Error(w, "room required", http.StatusBadRequest)
		return
	}

	rows, err := db.Query(`
		SELECT sender, content, media_url, media_type, timestamp
		FROM (
			SELECT id, sender, content, media_url, media_type, timestamp
			FROM messages
			WHERE room_name = ?
			ORDER BY id DESC
			LIMIT 50
		) ORDER BY id ASC
	`, room)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type historyMessage struct {
		Type      string `json:"type"`
		Room      string `json:"room"`
		Sender    string `json:"sender"`
		Content   string `json:"content"`
		MediaURL  string `json:"mediaURL,omitempty"`
		MediaType string `json:"mediaType,omitempty"`
		Timestamp string `json:"timestamp"`
	}
	msgs := []historyMessage{}
	for rows.Next() {
		var m historyMessage
		var mediaURL, mediaType string
		if err := rows.Scan(&m.Sender, &m.Content, &mediaURL, &mediaType, &m.Timestamp); err != nil {
			http.Error(w, "scan error", http.StatusInternalServerError)
			return
		}
		m.Type = "chat"
		m.Room = room
		m.MediaURL = mediaURL
		m.MediaType = mediaType
		msgs = append(msgs, m)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"messages": msgs})
}

// handleRooms manages the persistent room list for the authenticated user.
//
//	GET    → {"rooms": ["general", "random"]}  (ordered by join time)
//	POST   body {"room":"name"} → 204          (idempotent via INSERT OR IGNORE)
//	DELETE body {"room":"name"} → 204
func handleRooms(w http.ResponseWriter, r *http.Request) {
	userID, ok := validateSessionParam(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		rows, err := db.Query(
			"SELECT room_name FROM user_rooms WHERE user_id = ? ORDER BY rowid ASC", userID)
		if err != nil {
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		rooms := []string{} // initialised as slice (not nil) so JSON encodes as [] not null
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				http.Error(w, "scan error", http.StatusInternalServerError)
				return
			}
			rooms = append(rooms, name)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string][]string{"rooms": rooms})

	case http.MethodPost:
		var req struct {
			Room string `json:"room"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Room == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if _, err := db.Exec(
			"INSERT OR IGNORE INTO user_rooms (user_id, room_name) VALUES (?, ?)",
			userID, req.Room); err != nil {
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case http.MethodDelete:
		var req struct {
			Room string `json:"room"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Room == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if _, err := db.Exec(
			"DELETE FROM user_rooms WHERE user_id = ? AND room_name = ?",
			userID, req.Room); err != nil {
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleConnection upgrades an HTTP request to a WebSocket and manages the
// full lifecycle of a chat connection.
//
// Query params:
//   - session: required — UUID session token from /login
//
// Session validation happens before the WebSocket upgrade so that rejected
// connections receive a proper HTTP 401 rather than a WebSocket close frame.
// No room is auto-joined on connect; the client sends joinRoom messages after
// fetching its saved rooms via GET /rooms.
func handleConnection(w http.ResponseWriter, r *http.Request) {
	// Validate session before upgrading — http.Error won't work after Upgrade.
	userID, ok := validateSessionParam(w, r)
	if !ok {
		return
	}
	user, err := lookupUsername(userID)
	if err != nil {
		http.Error(w, "user not found", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade error:", err)
		return
	}
	defer conn.Close()
	defer hub.removeFromAll(conn) // clean up all room memberships on disconnect

	log.Printf("client connected: %s", user)

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			// Notify all rooms the user was in before removing them from the hub.
			rooms := hub.roomsForConn(conn)
			disconnectMessage := stamp(protocol.Message{
				Type:    protocol.TypeSystem,
				Content: fmt.Sprintf("*** %s disconnected ***", user),
			})
			for _, rm := range rooms {
				hub.broadcastToRoom(disconnectMessage, conn, rm)
			}

			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("unexpected error for %s: %v", user, err)
			} else {
				log.Printf("%s disconnected", user)
			}
			return
		}

		handleMessage(raw, conn, user)
	}
}

// handleMessage parses a raw WebSocket frame and acts on its message type.
// user is the server-verified username of the sender; it overrides any sender
// field supplied by the client to prevent identity spoofing.
func handleMessage(raw []byte, conn *websocket.Conn, user string) {
	var message protocol.Message
	if err := json.Unmarshal(raw, &message); err != nil {
		log.Printf("unmarshal error: %v | raw: %q", err, string(raw))
		return
	}

	// Always stamp on arrival so the server is the authority on time.
	message = stamp(message)

	switch message.Type {
	case protocol.TypeSystem:
		// Clients cannot emit system messages; ignore to prevent spoofing.
		log.Println("received system message (ignored)")

	case protocol.TypeChat:
		// Override sender with the authenticated username.
		message.Sender = user
		hub.broadcastToRoom(message, conn, message.Room)
		saveMessage(message)

	case protocol.TypeJoinRoom:
		hub.addToRoom(conn, user, message.Room)
		hub.broadcastToRoom(stamp(protocol.Message{
			Type:    protocol.TypeSystem,
			Room:    message.Room,
			Content: fmt.Sprintf("%s joined the room", user),
		}), conn, message.Room)

	case protocol.TypeLeaveRoom:
		hub.removeFromRoom(conn, message.Room)
		hub.broadcastToRoom(stamp(protocol.Message{
			Type:    protocol.TypeSystem,
			Room:    message.Room,
			Content: fmt.Sprintf("%s left the room", user),
		}), conn, message.Room)

	default:
		log.Printf("unknown message type: %q", message.Type)
	}
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	certFile := flag.String("cert", "", "TLS certificate file (PEM)")
	keyFile := flag.String("key", "", "TLS key file (PEM)")
	squadName := flag.String("squad-name", "My Squad", "Squad name shown to all members (used only on first run)")
	mediaDirFlag := flag.String("media-dir", "./media", "directory for persisted media uploads")
	flag.Parse()

	mediaDir = *mediaDirFlag
	if err := os.MkdirAll(mediaDir, 0755); err != nil {
		log.Fatal("failed to create media directory:", err)
	}

	var err error
	db, err = database.Open("sethchat.db")
	if err != nil {
		log.Fatal("failed to open database:", err)
	}
	defer db.Close()

	// Seed the squad row on first run; INSERT OR IGNORE is a no-op thereafter.
	if _, err = db.Exec(`INSERT OR IGNORE INTO squad (id, name) VALUES (1, ?)`, *squadName); err != nil {
		log.Fatal("failed to init squad:", err)
	}

	http.HandleFunc("/register", handleRegister)
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/rooms", handleRooms)
	http.HandleFunc("/history", handleHistory)
	http.HandleFunc("/squad", handleSquad)
	http.HandleFunc("/squad/members", handleSquadMembers)
	http.HandleFunc("/upload", handleUpload)
	http.HandleFunc("/media/", handleMedia)
	http.HandleFunc("/ws", handleConnection)
	http.Handle("/", http.FileServer(http.Dir("./web")))

	if *certFile != "" && *keyFile != "" {
		fmt.Printf("server listening on %s (TLS)\n", *addr)
		log.Fatal(http.ListenAndServeTLS(*addr, *certFile, *keyFile, nil))
	} else {
		fmt.Printf("server listening on %s (plaintext)\n", *addr)
		log.Fatal(http.ListenAndServe(*addr, nil))
	}
}
