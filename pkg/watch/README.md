# Watch Package REST APIs

This package provides REST APIs for managing watchers and webhooks.

## Base Path
`/watch`

## Watcher Management

### Add Webhook Watcher
- **POST** `/watch/add/{name}?url={url}`

### WebSocket Connection
- **GET** `/watch/ws`
  - Establishes a WebSocket connection for real-time event watching

## Notes
- `{name}` - Unique name for the watcher
- `url` query parameter - Webhook URL to send events to
- WebSocket endpoint provides real-time event streaming
