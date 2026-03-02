package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sethchat/internal/protocol"

	"github.com/gorilla/websocket"
)

var (
	user string
	room string
)

func main() {
	flag.StringVar(&user, "user", "", "your username (required)")
	flag.StringVar(&room, "room", "general", "chat room to join")
	flag.Parse()

	if user == "" {
		log.Fatal("--user is required")
	}

	url := fmt.Sprintf("ws://localhost:8080/ws?user=%s&room=%s", user, room)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		log.Fatal("dial error:", err)
	}
	defer conn.Close()

	fmt.Printf("connected to server as %s in #%s\n", user, room)

	joinMsg := protocol.Message{
		Type:   protocol.TypeJoinRoom,
		Sender: user,
		Room:   room,
	}
	if err := sendMessage(conn, joinMsg); err != nil {
		log.Fatal("join error:", err)
	}

	// Receive messages in background.
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				log.Println("disconnected:", err)
				os.Exit(0)
			}
			var message protocol.Message
			if err := json.Unmarshal(raw, &message); err != nil {
				log.Printf("unmarshal error: %v | raw: %q", err, string(raw))
				continue
			}
			printMessage(message)
		}
	}()

	// Send messages from stdin.
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		msg := protocol.Message{
			Type:    protocol.TypeChat,
			Sender:  user,
			Room:    room,
			Content: scanner.Text(),
		}
		if err := sendMessage(conn, msg); err != nil {
			log.Println("write error:", err)
			return
		}
	}
}

func sendMessage(conn *websocket.Conn, msg protocol.Message) error {
	bytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, bytes)
}

func printMessage(message protocol.Message) {
	switch message.Type {
	case protocol.TypeSystem:
		fmt.Println(message.Content)
	case protocol.TypeChat:
		fmt.Printf("[%s] %s: %s\n", message.Room, message.Sender, message.Content)
	}
}
