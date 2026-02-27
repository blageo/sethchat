package protocol

type MessageType string

const TypeChat MessageType = "chat"
const TypeSystem MessageType = "system"

type Message struct {
	Type    MessageType `json:"type"`
	Sender  string      `json:"sender,omitempty"`
	Content string      `json:"content"`
}
