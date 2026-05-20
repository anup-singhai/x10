# x10 Codebase Overview

x10 is a **lightning-fast multi-agent coding CLI** written in Go. It directly calls LLMs (Anthropic/OpenAI) with minimal overhead and zero middleware.

## Core Architecture

### Entry Point

- **`cmd/x10/main.go`** — CLI entrypoint with command parsing and flags

### Agent System

- **`agent/agent.go`** — Main `Agent` type that runs tasks via `loop()` method
  - Handles multi-turn LLM conversations
  - Pre-injects codebase context (zero LLM round trips for first message)
  - Supports image analysis (base64 embedded in task)
  - Streams responses through event channels

### LLM Providers

- **`providers/anthropic.go`** — Anthropic API client (Claude models)
- **`providers/openai.go`** — OpenAI API client (GPT models)
- **`providers/types.go`** — Shared request/response types and message structures

### Tools & Capabilities

- **`tools/tools.go`** — Tool registry and execution
- **`tools/index_tools.go`** — Codebase indexing tools
  - `codebase_search` — find symbols by semantic search
  - `symbol_lookup` — find definitions by exact name
  - `read_file`, `write_file`, `edit_file` — file operations
  - Provides tools like `codebase_search`, `symbol_lookup`, `read_file`, etc.

### Codebase Indexing

- **`index/index.go`** — Core indexing engine
- **`index/fts5.go`** — Full-text search via SQLite FTS5
- **`index/symbol.go`** — Symbol extraction (functions, classes, methods)
- **`index/extractor.go`** — Code parsing and metadata extraction
- **`index/schema.go`** — Index schema definitions
- **`index/query.go`** — Query building for semantic searches
- **`index/context.go`** — Context building from queries
- **`index/watcher.go`** — File system watching for incremental updates

### Orchestration

- **`orchestrator/orchestrator.go`** — Multi-agent orchestration (spawn N agents in parallel on git worktrees)
- **`cell/local.go`** — Local workspace/cell management

### Configuration & UI

- **`config/config.go`** — API key and settings management
- **`ui/render.go`** — Terminal output rendering

## Key Design Principles

1. **No middleware** — Direct HTTP/2 streaming to LLM providers
2. **Pre-loaded context** — Codebase context built once, injected before first LLM call
3. **Parallel execution** — Multiple agents work simultaneously on isolated git worktrees
4. **Image support** — Base64 image data embedded in task prompts
5. **Fast startup** — Go binary (~5ms startup vs Node.js)

## File Structure

```
.gitignore
CHANGES.md
IMAGE_SUPPORT.md
Makefile
README.md
agent/agent.go
cell/local.go
cmd/x10/main.go
config/config.go
go.mod
go.sum
index/
  context.go
  extractor.go
  fts5.go
  index.go
  query.go
  schema.go
  symbol.go
  watcher.go
orchestrator/orchestrator.go
providers/
  anthropic.go
  openai.go
  types.go
tools/
  index_tools.go
  tools.go
ui/render.go
```
