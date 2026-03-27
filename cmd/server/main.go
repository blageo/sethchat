package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
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

// handleRegister creates a new user account.
// Expects POST with JSON body: {"username": "...", "password": "..."}.
// Returns 201 on success; the client must then call /login to get a session.
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
	if err := auth.Register(db, req.Username, req.Password); err != nil {
		http.Error(w, "registration failed: "+err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusCreated)
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
	flag.Parse()

	var err error
	db, err = database.Open("sethchat.db")
	if err != nil {
		log.Fatal("failed to open database:", err)
	}
	defer db.Close()

	http.HandleFunc("/register", handleRegister)
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/rooms", handleRooms)
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
