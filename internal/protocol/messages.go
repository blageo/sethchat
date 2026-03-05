package protocol

import "time"

// MessageType distinguishes the purpose of a Message.
type MessageType string

const (
	TypeChat      MessageType = "chat"
	TypeSystem    MessageType = "system"
	TypeJoinRoom  MessageType = "joinRoom"
	TypeLeaveRoom MessageType = "leaveRoom"
)

// Message is the common envelope for all WebSocket communication.
type Message struct {
	Type      MessageType `json:"type"`
	Room      string      `json:"room"`
	Sender    string      `json:"sender,omitempty"`
	Content   string      `json:"content"`
	Timestamp time.Time   `json:"timestamp"`
}
