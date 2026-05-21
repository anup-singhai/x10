# x10

Lightning-fast multi-agent coding CLI. Direct LLM calls. No middleware. Open source.

[![x10 demo](https://img.youtube.com/vi/-O7IlQsEsF8/maxresdefault.jpg)](https://youtu.be/-O7IlQsEsF8)

```bash
x10 "fix the null pointer in auth.go"
x10 -n 3 "refactor the API layer, add tests, and update docs"
x10              # interactive REPL with conversation memory
```

## Why

Every coding agent today is slow because:

1. **Agent loop round trips** — LLM reads files one by one via tool calls. Each round trip = full API call + network latency
2. **No codebase context** — agent spends the first N turns just discovering what files exist
3. **Sequential** — one task at a time
4. **Middleware overhead** — Node.js startup, SDK abstractions, agent frameworks

x10 fixes all of these:

- **Go binary** — 5ms startup vs 300ms+ for Node.js
- **Direct HTTP/2 streaming** — raw SSE to Anthropic/OpenAI, no SDK
- **Pre-built context** — tree-sitter indexes your codebase locally into SQLite FTS5. Relevant code is injected before the first LLM call, not discovered via tool calls
- **Parallel tool execution** — all tool calls in a single LLM turn run concurrently
- **Multi-agent** — spawn N agents on isolated git worktrees, all working in parallel
- **Conversation memory** — REPL maintains full message history across turns

## Install

```bash
# from source (requires Go 1.24+)
git clone https://github.com/anup-singhai/x10
cd x10
make build          # builds with sqlite_fts5 support
make install        # installs to $GOPATH/bin
```

## Setup

```bash
x10 config set anthropic-key sk-ant-...
x10 config set openai-key sk-...
```

Or use environment variables:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
export OPENAI_API_KEY=sk-...
```

## Usage

```bash
# Interactive REPL (persistent conversation)
x10

# Single task
x10 "explain the auth flow"
x10 "add rate limiting to the API"

# Choose model
x10 -m claude-opus-4-6 "refactor auth.go"
x10 -m gpt-4o "review this PR"

# Multi-agent: 3 agents in parallel, each on isolated git worktree
x10 -n 3 --worktree "fix all TypeScript errors"

# Different workspace
x10 -d /path/to/project "add unit tests"

# Pipe text
cat error.log | x10 "what caused this error"

# Drop/pipe an image (vision models)
cat screenshot.png | x10 "fix the UI bug shown here"
x10 "what's wrong?" < error_dialog.png
```

### REPL commands

| Command  | Description                        |
|----------|------------------------------------|
| `/clear` | Clear conversation history         |
| `/exit`  | Exit                               |
| `Ctrl+C` | Exit                               |

## Codebase index

On first run, x10 builds a local symbol index of your workspace using tree-sitter. This index is used to pre-assemble relevant code context before calling the LLM — eliminating the exploration round trips that make other agents slow.

```bash
x10 index .          # build or rebuild the index
x10 --reindex        # force rebuild on next run
x10 --no-index       # disable indexing
```

Supported languages: **Go, TypeScript, TSX, JavaScript, Python, Rust** — more coming.

The index lives at `.x10/index.db` (SQLite + FTS5, local only, never leaves your machine).

## Supported models

| Provider  | Models                                             |
|-----------|----------------------------------------------------|
| Anthropic | `claude-haiku-4-5`, `claude-sonnet-4-6`, `claude-opus-4-6` |
| OpenAI    | `gpt-4o`, `gpt-4o-mini`, `o1`, `o3`, etc          |

Default: `claude-haiku-4-5-20251001` (fastest). Adaptive selection picks `claude-sonnet-4-6` or `claude-opus-4-6` for complex tasks automatically.

## Multi-agent

```bash
# 3 agents work in parallel, each on its own branch
x10 -n 3 --worktree "fix all lint errors"
# → branches: x10/agent-1-xxx, x10/agent-2-xxx, x10/agent-3-xxx
```

Each agent gets an isolated git worktree, its own tool context, and streams output back concurrently. The orchestrator fans all events into a single terminal stream labeled by agent ID.

## Architecture

```
x10/
├── cmd/x10/        CLI entrypoint (cobra) — REPL + single-shot + image input
├── providers/      Direct HTTP/2 SSE streaming — Anthropic, OpenAI (no SDK)
├── agent/          Stateful LLM + tool loop — parallel tool execution, conversation history
├── cell/           Isolated execution context — local process or git worktree
├── orchestrator/   Multi-agent fan-out + Session (persistent REPL)
├── tools/          read_file, write_file, edit_file, bash, glob, grep, list_dir
│                   + codebase_search, symbol_lookup (when index is active)
├── index/          Tree-sitter → SQLite FTS5 codebase indexer + file watcher
├── config/         Key management — ~/.x10/config.json + env vars
└── ui/             Live streaming renderer — inline markdown, spinner, multi-agent lanes
```

## Roadmap

- [x] Direct streaming providers (Anthropic, OpenAI)
- [x] Parallel tool execution within a turn
- [x] Multi-agent with git worktree isolation
- [x] Tree-sitter codebase indexer (Go, TypeScript, Python, Rust, JS)
- [x] Pre-built context injection (zero exploration round trips)
- [x] Conversation memory across REPL turns
- [x] Image/vision input (drag file or pipe)
- [x] Live markdown rendering + spinner
- [ ] Google Gemini provider
- [ ] Cloud cells via [oncell.ai](https://oncell.ai)
- [ ] `x10 pr` — open PRs from multi-agent results
- [ ] Java, Swift, Kotlin extractor

## License

MIT
