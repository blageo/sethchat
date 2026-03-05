package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"sethchat/internal/protocol"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

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

func handleConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade error:", err)
		return
	}
	defer conn.Close()

	user := r.URL.Query().Get("user")
	if user == "" {
		log.Println("rejected connection: user param required")
		return
	}
	room := r.URL.Query().Get("room")
	if room == "" {
		room = "general"
	}

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

		handleMessage(raw, conn)
	}
}

func handleMessage(raw []byte, conn *websocket.Conn) {
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
		hub.broadcastToRoom(message, conn, message.Room)

	case protocol.TypeJoinRoom:
		hub.addToRoom(conn, message.Sender, message.Room)
		hub.broadcastToRoom(stamp(protocol.Message{
			Type:    protocol.TypeSystem,
			Room:    message.Room,
			Content: fmt.Sprintf("%s joined the room", message.Sender),
		}), conn, message.Room)

	case protocol.TypeLeaveRoom:
		hub.removeFromRoom(conn, message.Room)
		hub.broadcastToRoom(stamp(protocol.Message{
			Type:    protocol.TypeSystem,
			Room:    message.Room,
			Content: fmt.Sprintf("%s left the room", message.Sender),
		}), conn, message.Room)

	default:
		log.Printf("unknown message type: %q", message.Type)
	}
}

func main() {
	http.HandleFunc("/ws", handleConnection)
	fmt.Println("server listening on :8080")
	http.Handle("/", http.FileServer(http.Dir("./web")))
	log.Fatal(http.ListenAndServe(":8080", nil))
}
