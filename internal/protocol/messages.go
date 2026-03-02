// Package protocol defines the shared message types used between the
// sethchat server and client over WebSocket.
package protocol

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
	Type    MessageType `json:"type"`
	Room    string      `json:"room"`
	Sender  string      `json:"sender,omitempty"`
	Content string      `json:"content"`
}
