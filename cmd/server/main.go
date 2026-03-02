package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"sethchat/internal/protocol"

	"github.com/gorilla/websocket"
)

// Upgrader is how HTTP requests are upgraded to websockets. GET request with extra headers
var upgrader = websocket.Upgrader{
	// This is for checking origin of connections in browser against whitelist
	// Since this is terminal based we will allow all?
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// Need a hub to collect client connections and broadcast
type Hub struct {
	// Registered clients. rooms mapped to conns mapped to user string
	clients map[string]map[*websocket.Conn]string
	mu      sync.Mutex
}

func (h *Hub) addToRoom(conn *websocket.Conn, user string, room string) {
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

func (h *Hub) removeFromAll(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for room := range h.clients {
		delete(h.clients[room], conn)
	}
}

func (h *Hub) broadcastToRoom(message protocol.Message, sender *websocket.Conn, room string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	log.Printf("broadcasting to room %s, %d clients", room, len(h.clients[room]))
	mes, err := json.Marshal(message)
	if err != nil {
		log.Println("marshal error:", err)
		return
	}
	for conn := range h.clients[room] {
		if conn != sender {
			err = conn.WriteMessage(websocket.TextMessage, mes)
			if err != nil {
				log.Println("write error:", err)
			}
		}
	}
}

var hub = Hub{clients: make(map[string]map[*websocket.Conn]string)}

// handler function that gets passed to http.HandleFunc()
func handleConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("upgrade error:", err)
		return
	}
	defer conn.Close()

	// add a hub to handle connections and broadcasting and save user
	user := r.URL.Query().Get("user")
	if user == "" {
		log.Println("user is required")
		return
	}
	// extract room from cli and apply general room if omitted
	room := r.URL.Query().Get("room")
	if room == "" {
		room = "general"
	}
	hub.addToRoom(conn, user, room)
	defer hub.removeFromAll(conn)

	welcomeMessage := protocol.Message{Type: protocol.TypeSystem, Content: fmt.Sprintf("*** %s connected to %s***", user, room)}
	log.Printf("client connected with username: %s", user)
	hub.broadcastToRoom(welcomeMessage, conn, room)

	// Read Loop
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			disconnectMessage := protocol.Message{Type: protocol.TypeSystem, Content: fmt.Sprintf("*** %s disconnected ***", user)}
			// loop over rooms
			for rm := range hub.clients {
				_, ok := hub.clients[rm][conn] // check for conn in room
				if ok {
					hub.broadcastToRoom(disconnectMessage, conn, rm) // broadcast disconnect to room if conn was present
				}
			}
			// server logging expected close errors include interupt signals and closing browser tabs etc.
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("unexpected error for %s: %v", user, err)
			} else {
				log.Printf("%s disconnected", user)
			}
			return
		}

		handleMessage(message, conn)
		// chatMessage := protocol.Message{Type: protocol.TypeChat, Sender: user, Content: string(message)}

		// log.Printf("received from %s: %s", hub.clients[room][conn], message)
		// hub.broadcastToRoom(chatMessage, conn, room)
	}
}

func handleMessage(mes []byte, conn *websocket.Conn) {
	message := protocol.Message{}
	err := json.Unmarshal(mes, &message)
	if err != nil {
		log.Printf("unmarshal error: %v | raw: %q", err, string(mes)) // log raw bytes
		return
	}
	switch message.Type {
	case protocol.TypeSystem:
		log.Println("This is a system message")
	case protocol.TypeChat:
		// log.Printf("chat received from %s: %s", message.Sender, message)
		hub.broadcastToRoom(message, conn, message.Room)
	case protocol.TypeJoinRoom:
		hub.addToRoom(conn, message.Sender, message.Room)
		joinMessage := protocol.Message{
			Type:    protocol.TypeSystem,
			Room:    message.Room,
			Content: fmt.Sprintf("%s joined the room", message.Sender),
		}
		hub.broadcastToRoom(joinMessage, conn, message.Room)
	case protocol.TypeLeaveRoom:
		hub.removeFromRoom(conn, message.Room)
		leaveMessage := protocol.Message{
			Type:    protocol.TypeSystem,
			Room:    message.Room,
			Content: fmt.Sprintf("%s left the room", message.Sender),
		}
		hub.broadcastToRoom(leaveMessage, conn, message.Room)
	}

}

func main() {
	http.HandleFunc("/ws", handleConnection)

	fmt.Println("server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
