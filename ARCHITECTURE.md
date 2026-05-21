# Core Architecture

## 1. **Agent Loop** (`agent/agent.go` → `Agent.loop`)
- Main execution engine that processes tasks via LLM streaming
- **Image Handling**: Detects and embeds base64-encoded images in tasks
  - Parses `[IMAGE]...[END_IMAGE]` blocks
  - Extracts media type and base64 data
  - Constructs multi-modal content blocks (image + text)
- **Context Injection**: Pre-injects codebase context via `contextBuilder` callback to **zero out LLM round trips**
  - Wraps context in `<context>` tags
  - Appends context as additional text block alongside task
  - Works for both image and text-only modes

## 2. **Intelligent Indexing** (`tools/index_tools.go` → `makeCodebaseContext`)
- Creates on-demand codebase context for tasks
- Dynamically builds symbol-rich context using `idx.BuildContext(task, maxSymbols)`
- Falls back gracefully with "no relevant symbols found" message
- Configurable result limits (default: 15 symbols)

## 3. **Dependency Walking** (`index/graph.go` → `Index.walkDeps`)
- Recursively traverses function call chains up to configurable depth
- Language-aware call extraction:
  - Go: function calls via AST analysis
  - TypeScript/JavaScript: import and function calls
  - Python: function definitions and calls
  - Default: JavaScript fallback
- Token budget enforcement: stops traversing when context size exceeds limit
- Builds dependency graph with visited tracking to prevent cycles

## 4. **CLI Entry Point** (`cmd/x10/main.go` → `main`)
- **Cobra-based CLI** with `run [task]` command
- **Flags**:
  - `--model`: Explicit LLM model selection
  - `--agents N`: Multi-agent parallelization
  - `--worktree`: Git worktree support
  - `--workdir`: Custom workspace directory
  - `--no-index`, `--index`: Control codebase indexing

## 5. **Image Input Processing**
- **`readStdinWithImageDetection`**: Detects piped image data via magic bytes
  - Supports: JPEG, PNG, GIF, WebP
  - Base64-encodes up to 5MB
  - Wraps in `[IMAGE]...[END_IMAGE]` format

- **`injectImageFromPath`**: Converts image file paths to embedded base64
  - Walks backward from file extension to find path start
  - Handles shell-escaped spaces (`\ `)
  - Extracts surrounding text as task context

## 6. **Diff Computation** (`tools/tools.go` → `computeLineDiff`)
- **LCS-based line diffing**: Longest Common Subsequence algorithm
- Smart truncation: shows context lines around changes
- Handles large files gracefully with line count summary
- Output format: `+` (added), `-` (removed), ` ` (context)
- Used in `editFile` to show compact diffs to UI

## 7. **Parallel Tool Execution** (`agent/agent.go` → `Agent.executeParallel`)
- Executes multiple tool calls concurrently with WaitGroup
- **Smart output compression**:
  - UI receives full diffs (e.g., `DIFF:path/to/file\n+lines...`)
  - LLM receives compact summary (e.g., `updated path/to/file`)
- Context-aware cancellation and error handling
- Maintains result order for tool_result blocks

## 8. **Agent State & History** (`agent/agent.go` → `Agent`)
- **Persistent conversation history**: `history []providers.Message` maintained across turns
- **Agent.Reset()**: Clears history between independent tasks
- **Agent.WithContextBuilder()**: Fluent API for injecting context builder
- **Multi-agent support**: Each agent has unique `ID`, independent history, and workdir
- **Streaming output**: Events emitted via channel (`EventToolCall`, `EventToolResult`, `EventError`)
- **Max rounds**: Default limit on conversation iterations (configurable)

## Key Design Patterns

| Pattern | Purpose |
|---------|---------|
| **Pre-injected Context** | Eliminate LLM calls for context lookup; embed relevant symbols upfront |
| **Multi-modal Content Blocks** | Support both images and text in single request |
| **Dependency Depth Limiting** | Prevent context explosion with token budgets and max hops |
| **Language-Aware Extraction** | File extension → call extractor mapping |
| **LCS Diffing** | Efficient line-level change visualization |
| **Diff Compression** | Full output for UI, compact summaries for LLM history |

## Data Flow

```
User Input (task + optional image)
    ↓
[Image Detection] → base64 encode + wrap in [IMAGE] block
    ↓
[Context Injection] → pre-load relevant symbols from index
    ↓
[Agent.loop] → construct multi-block content (image + context + task)
    ↓
[LLM Provider] → stream response with tool use calls
    ↓
[Agent.executeParallel] → concurrently run tools, compress output
    ↓
[Events Channel] → stream full output to client
```

## Agent Execution Model

### Loop Lifecycle (`Agent.loop`)
1. **Input Parsing**: Detects and extracts embedded images from `[IMAGE]...[END_IMAGE]` blocks
2. **Context Injection**: Calls `contextBuilder(task)` to pre-fetch relevant symbols (zero LLM calls)
3. **Content Assembly**: Builds multi-modal content blocks:
   - Image block + text block (if image present)
   - OR context block + text block (pure text mode)
4. **LLM Streaming**: Sends content to provider, receives tool use calls in response
5. **Tool Execution**: Routes parallel execution via `executeParallel()`
6. **Round Limit**: Enforces `maxRounds` to prevent infinite loops
7. **History Persistence**: Appends each message to agent's `history []providers.Message`

### Event Streaming (`chan Event`)
Four event types emitted during execution:

| Event Type | Fields | Example |
|---|---|---|
| `EventToolCall` | `AgentID`, `Action` | `Action: "readFile({"path":"main.go"})"` |
| `EventToolResult` | `AgentID`, `Result` | `Result: "DIFF:main.go\n+func new() {...}\n-func old() {...}"` |
| `EventError` | `AgentID`, `Error` | `Error: context.DeadlineExceeded` |
| `EventMessage` | `AgentID`, `Message` | `Message: "I've made the following changes..."` |

**Result Format Details:**
- **File edits**: `DIFF:path/to/file\n+added lines\n-removed lines\n context`
- **File reads**: Raw file content as string
- **Tool errors**: `error: reason message`
- **LLM responses**: Streamed text from provider

### Output Compression Strategy
- **UI Layer**: Receives `DIFF:path/to/file\n+added\n-removed` (for rich rendering)
- **LLM History**: Compresses to `updated path/to/file` (token-efficient)
- **Non-diff output**: Passed through unchanged
- **Error output**: Wrapped as `error: ...` and sent to LLM

### Parallel Execution Semantics (`executeParallel`)
```go
for each tool call {
    emit EventToolCall
    execute tool concurrently
    emit EventToolResult (with full output)
    store compact llmOutput
}
// Build tool_result blocks in original order using llmOutput for LLM
return []interface{} with "tool_result" type for next LLM turn
```

## Agent Output Format

### Event Channel Structure
```go
type Event struct {
    Type    string      // "tool_call", "tool_result", "error", "message"
    AgentID string      // Unique agent identifier
    Action  string      // Tool call details (EventToolCall only)
    Result  string      // Full tool output (EventToolResult only)
    Message string      // LLM response text (EventMessage only)
    Error   error       // Error details (EventError only)
}
```

### Output Examples by Tool Type

**File Read Output:**
```
Result: "package main\n\nfunc main() {\n    fmt.Println(\"Hello\")\n}"
```

**File Edit Output (DIFF format):**
```
Result: "DIFF:cmd/x10/main.go
-func oldHandler() {
- return "invalid"
 }
+func newHandler() {
+ return "valid"
 }
"
```
- Prefix: `DIFF:` + relative path
- Format: `+` added, `-` removed, ` ` context
- Context: Configurable lines around changes (default: 2)
- Compression: Omits unchanged sections with `...(N lines)`

**Error Output:**
```
Result: "error: old_string not found in file.go"
```

**Tool Call Event (before execution):**
```
Action: "editFile({"path":"main.go","old_string":"foo","new_string":"bar"})"
```
- JSON-serialized arguments
- Emitted immediately before tool execution

### LLM History Compression
When results are stored in agent history for next LLM turn:

```go
// Full output to UI
Result: "DIFF:main.go\n+new line\n-old line"

// Compressed for LLM history
llmOutput: "updated main.go"
```

This keeps conversation history compact while UIs can render detailed diffs.

## Optimization Highlights

✅ **Zero extra LLM round trips** for context (pre-injected locally)  
✅ **Streaming responses** via event channels  
✅ **Lazy index loading** only when needed  
✅ **Image auto-detection** from stdin or file paths  
✅ **Parallel tool execution** with smart output compression  
✅ **Token budgeting** in dependency walking  
✅ **LCS diffing** for human-readable code diffs
