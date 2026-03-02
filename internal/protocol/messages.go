package protocol

type MessageType string

const TypeChat MessageType = "chat"
const TypeSystem MessageType = "system"
const TypeJoinRoom MessageType = "joinRoom"
const TypeLeaveRoom MessageType = "leaveRoom"

type Message struct {
	Type    MessageType `json:"type"`
	Room    string      `json:"room"`
	Sender  string      `json:"sender,omitempty"`
	Content string      `json:"content"`
}
