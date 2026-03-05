package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sethchat/internal/protocol"
	"strings"

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
	fmt.Println("commands: /join <room>, /leave, /room")

	if err := joinRoom(conn, room); err != nil {
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
		text := scanner.Text()
		if strings.HasPrefix(text, "/") {
			handleCommand(conn, text)
			continue
		}
		if room == "" {
			fmt.Println("not in a room. use /join <room>")
			continue
		}
		msg := protocol.Message{
			Type:    protocol.TypeChat,
			Sender:  user,
			Room:    room,
			Content: text,
		}
		if err := sendMessage(conn, msg); err != nil {
			log.Println("write error:", err)
			return
		}
	}
}

func handleCommand(conn *websocket.Conn, input string) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return
	}

	switch parts[0] {
	case "/join":
		if len(parts) < 2 {
			fmt.Println("usage: /join <room>")
			return
		}
		newRoom := parts[1]
		if newRoom == room {
			fmt.Printf("already in #%s\n", room)
			return
		}
		if room != "" {
			if err := leaveRoom(conn, room); err != nil {
				log.Println("leave error:", err)
				return
			}
		}
		if err := joinRoom(conn, newRoom); err != nil {
			log.Println("join error:", err)
			return
		}
		room = newRoom

	case "/leave":
		if room == "" {
			fmt.Println("not currently in a room")
			return
		}
		if err := leaveRoom(conn, room); err != nil {
			log.Println("leave error:", err)
			return
		}
		room = ""
		fmt.Println("left room. use /join <room> to rejoin")

	case "/room":
		if room == "" {
			fmt.Println("not currently in a room")
		} else {
			fmt.Printf("current room: #%s\n", room)
		}

	default:
		fmt.Printf("unknown command %q. commands: /join <room>, /leave, /room\n", parts[0])
	}
}

func joinRoom(conn *websocket.Conn, r string) error {
	return sendMessage(conn, protocol.Message{
		Type:   protocol.TypeJoinRoom,
		Sender: user,
		Room:   r,
	})
}

func leaveRoom(conn *websocket.Conn, r string) error {
	return sendMessage(conn, protocol.Message{
		Type:   protocol.TypeLeaveRoom,
		Sender: user,
		Room:   r,
	})
}

func sendMessage(conn *websocket.Conn, msg protocol.Message) error {
	bytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, bytes)
}

func printMessage(message protocol.Message) {
	ts := ""
	if !message.Timestamp.IsZero() {
		ts = fmt.Sprintf("[%s] ", message.Timestamp.Local().Format("15:04:05"))
	}
	switch message.Type {
	case protocol.TypeSystem:
		fmt.Printf("%s%s\n", ts, message.Content)
	case protocol.TypeChat:
		fmt.Printf("%s[%s] %s: %s\n", ts, message.Room, message.Sender, message.Content)
	}
}
