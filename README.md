# sethchat

A self-hosted group chat application built with Go and WebSockets. Designed to be simple, lightweight, and deployable for small groups who want their own private chat without relying on third-party platforms.

> ⚠️ **Note:** Communications are not yet end-to-end encrypted. Authentication is not yet implemented. Not recommended for sensitive communications at this stage.

---

## Features

- Real-time messaging over WebSockets
- Multiple chat rooms
- Server-side timestamps for accurate message ordering
- Browser-based client — no install required for users
- CLI client for terminal use
- Join/leave room notifications

## Project Structure

```
sethchat/
├── cmd/
│   ├── server/
│   │   └── main.go            # WebSocket server entry point
│   └── client/
│       └── main.go            # CLI client entry point
├── internal/
│   └── protocol/
│       └── messages.go        # Shared message types
└── web/
    ├── index.html             # Browser client
    ├── css/
    │   └── style.css          # Styling
    └── js/
        └── script.js          # Client-side logic

```

## Getting Started

### Prerequisites

- Go 1.21+

### Running the server

```bash
go run ./cmd/server/main.go
```

The server listens on `:8080` and serves the web client at `http://localhost:8080`.

### Using the browser client

Open `http://localhost:8080` in your browser, enter a username and room name, and connect.

### Using the CLI client

```bash
go run ./cmd/client/main.go --user yourname --room general
```

**CLI commands:**

| Command | Description |
|---|---|
| `/join <room>` | Leave current room and join a new one |
| `/leave` | Leave the current room |
| `/room` | Display the current room |

## Protocol

Messages are JSON-encoded WebSocket frames. All messages share a common envelope:

```json
{
  "type": "chat",
  "room": "general",
  "sender": "seth",
  "content": "hello",
  "timestamp": "2026-03-06T14:32:00Z"
}
```

| Type | Description |
|---|---|
| `chat` | A user message |
| `system` | Server notification (join, leave, connect) |
| `joinRoom` | Request to join a room |
| `leaveRoom` | Request to leave a room |

Timestamps are set server-side in UTC on message arrival.

## Roadmap

- [ ] Authentication (registration, login, sessions)
- [ ] Message persistence (database)
- [ ] HTTPS / WSS support
- [ ] Media sharing (images, GIFs)
- [ ] Docker packaging for easy self-hosting
- [ ] Mobile-friendly UI polish

## Built With

- [Go](https://golang.org/)
- [gorilla/websocket](https://github.com/gorilla/websocket)
- Vanilla HTML/CSS/JS