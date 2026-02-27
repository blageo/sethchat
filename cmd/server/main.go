package main

import (
	"fmt"
	"log"
	"net/http"
	"sync"

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
	// Registered clients.
	clients map[*websocket.Conn]string
	mu      sync.Mutex
}

func (h *Hub) add(conn *websocket.Conn, user string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[conn] = user
}

func (h *Hub) remove(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, conn)
}

func (h *Hub) broadcast(message []byte, sender *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for conn := range h.clients {
		if conn != sender {
			err := conn.WriteMessage(websocket.TextMessage, []byte(h.clients[sender]+": "+string(message)))
			if err != nil {
				log.Println("write error:", err)
			}
		}
	}
}

var hub = Hub{clients: make(map[*websocket.Conn]string)}

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
	hub.add(conn, user)
	defer hub.remove(conn)

	log.Printf("client connected with username: %s", user)
	hub.broadcast([]byte(fmt.Sprintf("*** %s joined ***", user)), conn)
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("client disconnected with username: %s, error: %v", hub.clients[conn], err)
			return
		}

		log.Printf("received from %s: %s", hub.clients[conn], message)
		hub.broadcast(message, conn)
	}
}

func main() {
	http.HandleFunc("/ws", handleConnection)

	fmt.Println("server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
