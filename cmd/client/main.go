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

var user string
var room string

func main() {
	// parse flags
	flag.StringVar(&user, "user", "", "your username")
	flag.StringVar(&room, "room", "", "room")
	flag.Parse()
	if user == "" {
		log.Fatal("user is required")
	}
	if room == "" {
		room = "general"
	}

	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:8080/ws?user="+user, nil)
	if err != nil {
		log.Fatal("dial error:", err)
	}

	defer conn.Close()

	fmt.Println("connected to server")

	// join room after connecting
	if room != "" {
		joinMsg := protocol.Message{
			Type:   protocol.TypeJoinRoom,
			Sender: user,
			Room:   room,
		}
		bytes, err := json.Marshal(joinMsg)
		if err != nil {
			log.Fatal("marshal error:", err)
		}
		err = conn.WriteMessage(websocket.TextMessage, bytes)
		if err != nil {
			log.Fatal("join error:", err)
		}
	}

	// asyncly prints incoming messages
	go func() {
		for {
			_, mes, err := conn.ReadMessage()
			if err != nil {
				log.Println("disconnected:", err)
				os.Exit(0)
			}
			message := protocol.Message{}
			err = json.Unmarshal(mes, &message)
			if err != nil {
				log.Printf("unmarshal error: %v | raw: %q", err, string(mes)) // log raw bytes
				continue
			}
			printMessage(message)
		}
	}()

	// read from stdin and send
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		msg := protocol.Message{
			Type:    protocol.TypeChat,
			Sender:  user,
			Room:    room, // whatever room flag you're using
			Content: scanner.Text(),
		}
		bytes, err := json.Marshal(msg)
		if err != nil {
			log.Println("marshal error:", err)
			continue
		}
		err = conn.WriteMessage(websocket.TextMessage, bytes)
		if err != nil {
			log.Println("write error:", err)
			return
		}
	}
}

func printMessage(message protocol.Message) {
	switch message.Type {
	case protocol.TypeSystem:
		fmt.Println(message.Content)
	case protocol.TypeChat:
		fmt.Printf("[%s] %s: %s\n", message.Room, message.Sender, message.Content)
	}
}
