package main

import (
	"database/sql"
	"encoding/json"
	"errors"
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

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

var db *sql.DB

// Hub manages all active WebSocket connections, organized by room.
type Hub struct {
	clients map[string]map[*websocket.Conn]string // room -> conn -> username
	mu      sync.RWMutex
}

func (h *Hub) addToRoom(conn *websocket.Conn, user, room string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[room] == nil {
		h.clients[room] = make(map[*websocket.Conn]string)
	}
	h.clients[room][conn] = user
}

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

func (h *Hub) removeFromAll(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for room := range h.clients {
		delete(h.clients[room], conn)
	}
}

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
func stamp(m protocol.Message) protocol.Message {
	if m.Timestamp.IsZero() {
		m.Timestamp = time.Now().UTC()
	}
	return m
}

var hub = Hub{clients: make(map[string]map[*websocket.Conn]string)}

func lookupUsername(userID int64) (string, error) {
	var name string
	err := db.QueryRow("SELECT name FROM users WHERE user_id = ?", userID).Scan(&name)
	return name, err
}

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

func handleConnection(w http.ResponseWriter, r *http.Request) {
	// Validate session before upgrading — http.Error won't work after Upgrade
	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		http.Error(w, "session required", http.StatusUnauthorized)
		return
	}
	userID, err := auth.ValidateSession(db, sessionID)
	if err != nil {
		if errors.Is(err, auth.ErrSessionExpired) {
			http.Error(w, "session expired", http.StatusUnauthorized)
		} else {
			http.Error(w, "invalid session", http.StatusUnauthorized)
		}
		return
	}
	user, err := lookupUsername(userID)
	if err != nil {
		http.Error(w, "user not found", http.StatusUnauthorized)
		return
	}
	room := r.URL.Query().Get("room")
	if room == "" {
		room = "general"
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade error:", err)
		return
	}
	defer conn.Close()

	hub.addToRoom(conn, user, room)
	defer hub.removeFromAll(conn)

	welcomeMessage := stamp(protocol.Message{
		Type:    protocol.TypeSystem,
		Content: fmt.Sprintf("*** %s connected to %s ***", user, room),
	})
	hub.broadcastToRoom(welcomeMessage, conn, room)
	log.Printf("client connected: %s in %s", user, room)

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
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
		log.Println("received system message (ignored)")

	case protocol.TypeChat:
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
	var err error
	db, err = database.Open("sethchat.db")
	if err != nil {
		log.Fatal("failed to open database:", err)
	}
	defer db.Close()

	http.HandleFunc("/register", handleRegister)
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/ws", handleConnection)
	fmt.Println("server listening on :8080")
	http.Handle("/", http.FileServer(http.Dir("./web")))
	log.Fatal(http.ListenAndServe(":8080", nil))
}
