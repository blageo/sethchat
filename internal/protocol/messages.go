package protocol

import "time"

// MessageType distinguishes the purpose of a Message.
type MessageType string

const (
	TypeChat         MessageType = "chat"
	TypeSystem       MessageType = "system"
	TypeJoinRoom     MessageType = "joinRoom"
	TypeLeaveRoom    MessageType = "leaveRoom"
	TypeKeyRequest   MessageType = "keyRequest"   // new member needs room key
	TypeKeyDistribute MessageType = "keyDistribute" // member delivers encrypted room key
)

// Message is the common envelope for all WebSocket communication.
type Message struct {
	Type      MessageType `json:"type"`
	Room      string      `json:"room"`
	Sender    string      `json:"sender,omitempty"`
	Content   string      `json:"content"`
	MediaURL  string      `json:"mediaURL,omitempty"`  // optional: path to uploaded media
	MediaType string      `json:"mediaType,omitempty"` // optional: MIME type of media
	Timestamp time.Time   `json:"timestamp"`
	IV        string      `json:"iv,omitempty"`        // base64(12-byte AES-GCM nonce), set when Encrypted=true
	Encrypted bool        `json:"encrypted,omitempty"` // true when Content is AES-256-GCM ciphertext
	PublicKey string      `json:"publicKey,omitempty"` // used in keyRequest: sender's ECDH public key (base64 SPKI)
}
