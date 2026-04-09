# sethchat

A self-hosted group chat application built with Go and WebSockets. Designed to be simple, lightweight, and deployable for small groups who want their own private chat without relying on third-party platforms. This was built as a personal project for Boot.Dev.

> ⚠️ **Note:** Communications are not end-to-end encrypted. Not recommended for sensitive communications.

---

## Features

- Real-time messaging over WebSockets
- User registration and login with bcrypt password hashing and session tokens
- Squad structure — one server instance is one squad; users have owner, admin, or member roles
- Owner can rename the squad and manage member roles via the in-app UI
- Persistent room membership — your joined rooms are remembered across sessions
- Room sidebar with instant switching between rooms
- Media sharing — attach images, GIFs, and videos; paste screenshots directly from clipboard
- Server-side timestamps for accurate message ordering
- Browser-based client — no install required for users
- CLI client for terminal use
- Optional TLS (HTTPS/WSS) support

## Project Structure

```
sethchat/
├── cmd/
│   ├── server/
│   │   └── main.go            # WebSocket server entry point
│   └── client/
│       └── main.go            # CLI client entry point
├── internal/
│   ├── auth/
│   │   └── auth.go            # Registration, login, session management
│   ├── database/
│   │   └── db.go              # SQLite setup and schema
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

The server listens on `:8080` and serves the web client at `http://localhost:8080`. A `sethchat.db` SQLite database is created automatically in the working directory on first run.

**First-run squad setup:** The first user to register becomes the squad owner. Use `-squad-name` to set the squad name on initial startup (ignored on subsequent runs):

```bash
go run ./cmd/server/main.go -squad-name "My Squad"
```

### Running with TLS

```bash
# Generate a self-signed certificate (for local/private use)
openssl req -x509 -newkey rsa:4096 -keyout key.pem -out cert.pem -days 365 -nodes -subj '/CN=localhost'

# Start the server with TLS
go run ./cmd/server/main.go -cert cert.pem -key key.pem

# Optional: specify a custom address
go run ./cmd/server/main.go -addr :443 -cert cert.pem -key key.pem
```

When running over HTTPS, the browser client automatically upgrades WebSocket connections to `wss://`.

### Using the browser client

Open `http://localhost:8080`, register an account or log in, then use the sidebar to join rooms. Rooms are persisted to the database and restored on your next login.

**Attaching media:**
- Click the 📎 button to select a file from disk
- Paste an image directly from the clipboard (`Ctrl+V` / `Cmd+V`) while the message box is focused

### CLI client

```bash
go run ./cmd/client/main.go --user yourname --room general
```

> Note: The CLI client connects without authentication and is intended for local development use.

## Protocol

Messages are JSON-encoded WebSocket frames:

```json
{
  "type": "chat",
  "room": "general",
  "sender": "seth",
  "content": "hello",
  "mediaURL": "/media/abc123",
  "mediaType": "image/png",
  "timestamp": "2026-03-06T14:32:00Z"
}
```

| Field | Description |
|---|---|
| `type` | `chat`, `system`, `joinRoom`, or `leaveRoom` |
| `room` | Target room name |
| `sender` | Username (set server-side; client value is ignored) |
| `content` | Message text (optional if `mediaURL` is set) |
| `mediaURL` | Path to an uploaded media file (optional) |
| `mediaType` | MIME type of the media (optional) |
| `timestamp` | UTC timestamp, set server-side on arrival |

## Roadmap

- [x] Authentication (registration, login, sessions)
- [x] Server structure (squads) i.e. collections of rooms.
- [x] Message persistence (database)
- [x] HTTPS / WSS support
- [x] Media sharing (images, GIFs)
- [ ] Docker packaging for easy self-hosting
- [ ] Mobile-friendly UI polish
- [ ] E2EE

## Built With

- [Go](https://golang.org/)
- [gorilla/websocket](https://github.com/gorilla/websocket)
- [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) — pure Go SQLite driver
- Vanilla HTML/CSS/JS
