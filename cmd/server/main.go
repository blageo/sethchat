package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

// Upgrader is how HTTP requests are upgraded to websockets. GET request with extra headers
var upgrader = websocket.Upgrader{
	// This is for checking origin of connections in browser against whitelist
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// handler function that gets passed to http.HandleFunc()
func handleConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("upgrade error:", err)
		return
	}
	defer conn.Close()

	log.Println("client connected")

	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			log.Println("client disconnected", err)
		}

		log.Printf("received: %s", message)

		err = conn.WriteMessage(messageType, message)
		if err != nil {
			log.Println("write error:", err)
			return
		}
	}
}

func main() {
	http.HandleFunc("/ws", handleConnection)

	fmt.Println("server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
