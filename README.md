# terminus-pty

PTY server with session pooling for terminus-web. Drop-in replacement for `opencode serve` with reconnection support.

## Features

- **Session Pooling**: Sessions survive disconnections for 30 seconds (configurable)
- **Multi-Client Support**: Multiple WebSocket connections to the same PTY session
- **API Compatible**: Same endpoints as `opencode serve` (`/pty`, `/pty/:id`, `/pty/:id/connect`)
- **Basic Auth**: Optional authentication (compatible with terminus-web)

## Installation

```bash
go install github.com/itsmylife44/terminus-pty@latest
```

Or build from source:

```bash
git clone https://github.com/itsmylife44/terminus-pty.git
cd terminus-pty
make build
sudo make install
```

## Usage

```bash
terminus-pty [options]
```

### Options

| Flag                | Default                 | Description                           |
| ------------------- | ----------------------- | ------------------------------------- |
| `-port`             | `3001`                  | Port to listen on                     |
| `-host`             | `127.0.0.1`             | Host to bind to                       |
| `-session-timeout`  | `30s`                   | Session pool timeout after disconnect |
| `-cleanup-interval` | `10s`                   | Session cleanup interval              |
| `-shell`            | `$SHELL` or `/bin/bash` | Shell to use                          |
| `-auth-user`        | -                       | Basic auth username (optional)        |
| `-auth-pass`        | -                       | Basic auth password (optional)        |
| `-version`          | -                       | Show version                          |

### Examples

```bash
# Basic usage
terminus-pty --host 0.0.0.0 --port 3001

# With authentication
terminus-pty --auth-user admin --auth-pass secret

# Custom session timeout (5 minutes)
terminus-pty --session-timeout 5m

# Custom shell
terminus-pty --shell /bin/zsh
```

## API Endpoints

| Method   | Endpoint           | Description            |
| -------- | ------------------ | ---------------------- |
| `GET`    | `/health`          | Health check           |
| `POST`   | `/pty`             | Create new PTY session |
| `PUT`    | `/pty/:id`         | Resize PTY             |
| `DELETE` | `/pty/:id`         | Kill PTY session       |
| `GET`    | `/pty/:id/connect` | WebSocket connection   |

### Create Session

```bash
curl -X POST http://localhost:3001/pty \
  -H "Content-Type: application/json" \
  -d '{"cols": 80, "rows": 24}'
```

Response:

```json
{ "id": "pty_abc123" }
```

### Resize

```bash
curl -X PUT http://localhost:3001/pty/pty_abc123 \
  -H "Content-Type: application/json" \
  -d '{"size": {"cols": 120, "rows": 40}}'
```

### WebSocket Connect

```javascript
const ws = new WebSocket("ws://localhost:3001/pty/pty_abc123/connect");
ws.onmessage = (e) => terminal.write(e.data);
terminal.onData((data) => ws.send(data));
```

## Integration with terminus-web

Replace `opencode serve` with `terminus-pty` in your deployment:

```bash
# Before
opencode serve --port 3001 --hostname 0.0.0.0

# After
terminus-pty --port 3001 --host 0.0.0.0 --auth-user admin --auth-pass yourpassword
```

## Architecture

```
                    ┌─────────────────────────────────┐
                    │         SessionPool             │
                    │  ┌─────────────────────────┐    │
Client 1 ──WebSocket──▶│  Session (pty_abc123)   │    │
                    │  │  - PTY Process          │    │
Client 2 ──WebSocket──▶│  - Broadcast Channel    │    │
                    │  │  - DisconnectedAt       │    │
                    │  └─────────────────────────┘    │
                    │                                 │
                    │  Cleanup goroutine (10s)        │
                    │  - Remove sessions idle > 30s   │
                    └─────────────────────────────────┘
```

## License

MIT
