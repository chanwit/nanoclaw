# NanoClaw Go

Go implementation of NanoClaw - a personal Claude assistant that runs on WhatsApp with container-based isolation.

## Overview

This is a Go port of the original TypeScript NanoClaw implementation. It provides the same functionality with the benefits of Go's performance, static typing, and simpler deployment (single binary).

## Features

- WhatsApp integration via [whatsmeow](https://github.com/tulir/whatsmeow)
- Container-based agent isolation (Apple Container or Docker)
- Scheduled task execution with cron, interval, or one-time schedules
- IPC communication between host and containers
- SQLite database for message storage
- Mount security with allowlist validation

## Architecture

```
WhatsApp (whatsmeow) → SQLite Database → Polling Loop → Container (Claude Agent SDK) → Response
```

The architecture mirrors the original TypeScript implementation:

- Single Go process handles WhatsApp connectivity and message routing
- Agents run in isolated Linux containers
- IPC via JSON files in shared directories
- Each group has isolated filesystem and session

## Prerequisites

- Go 1.22+
- Container runtime (Apple Container or Docker)
- Claude Agent SDK container image (`nanoclaw-agent:latest`)

## Installation

```bash
# Clone the repository
git clone https://github.com/nanoclaw/go-nanoclaw
cd go-nanoclaw

# Download dependencies
make deps

# Build
make build
```

## Usage

### Authentication

First, authenticate with WhatsApp:

```bash
make auth
# or
./bin/nanoclaw-auth
```

Scan the QR code with WhatsApp to link your device.

### Running

```bash
make run
# or
./bin/nanoclaw
```

### Development

```bash
# Run with hot reload
make dev

# Run tests
make test

# Format code
make fmt

# Lint
make lint
```

## Configuration

Configuration is via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `ASSISTANT_NAME` | `Andy` | Trigger word for the assistant |
| `POLL_INTERVAL` | `2000` | Message polling interval (ms) |
| `SCHEDULER_POLL_INTERVAL` | `60000` | Scheduler check interval (ms) |
| `IPC_POLL_INTERVAL` | `1000` | IPC watcher interval (ms) |
| `CONTAINER_IMAGE` | `nanoclaw-agent:latest` | Container image name |
| `CONTAINER_TIMEOUT` | `300000` | Container execution timeout (ms) |
| `LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `TZ` | `UTC` | Timezone for cron schedules |

## Directory Structure

```
go-nanoclaw/
├── cmd/
│   ├── nanoclaw/main.go    # Main application
│   └── auth/main.go        # WhatsApp authentication
├── internal/
│   ├── config/             # Configuration management
│   ├── logger/             # Structured logging
│   ├── types/              # Shared type definitions
│   ├── db/                 # SQLite database operations
│   ├── security/           # Mount validation
│   ├── container/          # Container execution
│   ├── scheduler/          # Task scheduling
│   ├── whatsapp/           # WhatsApp client
│   ├── ipc/                # IPC handling
│   └── utils/              # Utility functions
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

## Runtime Directories

```
./
├── store/auth/             # WhatsApp credentials
├── groups/
│   ├── main/               # Main group (self-chat)
│   └── {group-folder}/     # Per-group directories
├── data/
│   ├── messages.db         # SQLite database
│   ├── router_state.json   # Polling state
│   ├── sessions.json       # Session mappings
│   ├── registered_groups.json
│   ├── sessions/           # Per-group sessions
│   ├── ipc/                # IPC directories
│   └── env/                # Environment files
```

## Differences from TypeScript Version

- **Single binary deployment** - No Node.js runtime required
- **Native concurrency** - Goroutines for parallel operations
- **Lower memory footprint** - Typically uses less memory than Node.js
- **Faster startup** - No JIT compilation needed
- **Static typing** - Compile-time type checking

## Container Agent

The container agent (running Claude Agent SDK) remains in TypeScript/Node.js since the Claude Agent SDK is a JavaScript library. The Go host communicates with it via stdin/stdout and file-based IPC.

## License

MIT
