package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/gorilla/websocket"
)

var user string

func main() {
	// parse flags
	flag.StringVar(&user, "user", "", "your username")
	flag.Parse()
	if user == "" {
		log.Fatal("user is required")
	}

	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:8080/ws?user="+user, nil)
	if err != nil {
		log.Fatal("dial error:", err)
	}

	defer conn.Close()

	fmt.Println("connected to server")

	// asyncly prints incoming messages
	go func() {
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Println("disconnected:", err)
				os.Exit(0)
			}
			fmt.Println(string(message))
		}
	}()

	// read from stdin and send
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		err := conn.WriteMessage(websocket.TextMessage, scanner.Bytes())
		if err != nil {
			log.Println("write error", err)
			return
		}
	}
}
