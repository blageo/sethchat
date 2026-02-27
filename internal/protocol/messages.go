package protocol

type MessageType string

const TypeChat MessageType = "chat"

type Message struct {
	Type    MessageType `json:"type"`
	Content string      `json:"content"`
}
