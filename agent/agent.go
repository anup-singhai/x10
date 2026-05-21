package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"x10/providers"
	"x10/tools"
)

// Event is an agent-level event streamed back to the caller.
type Event struct {
	Type    EventType
	AgentID string
	Text    string // for Token events
	Action  string // for ToolCall events
	Result  string // for ToolResult events
	Error   error  // for Error events
}

type EventType int

const (
	EventToken      EventType = iota
	EventToolCall
	EventToolResult
	EventDone
	EventError
)

// ContextBuilder assembles pre-built context for a task before the first LLM call.
// Returning a non-empty string injects it into the conversation so the model
// starts with relevant code already visible — no exploration round trips needed.
type ContextBuilder func(task string) string

const defaultMaxRounds = 15

// Agent runs an LLM + tool loop inside a workspace directory.
// It is stateful: history persists across Run calls so the REPL has memory.
type Agent struct {
	ID             string
	workdir        string
	provider       providers.Provider
	model          string
	tools          *tools.Registry
	system         string
	contextBuilder ContextBuilder
	maxRounds      int
	history        []providers.Message // persisted across turns
}

func New(id, workdir, model string, provider providers.Provider, registry *tools.Registry, system string) *Agent {
	return &Agent{
		ID:        id,
		workdir:   workdir,
		provider:  provider,
		model:     model,
		tools:     registry,
		system:    system,
		maxRounds: defaultMaxRounds,
	}
}

// Reset clears conversation history (e.g. on /clear).
func (a *Agent) Reset() { a.history = nil }

// WithContextBuilder attaches a context pre-builder to the agent.
func (a *Agent) WithContextBuilder(cb ContextBuilder) *Agent {
	a.contextBuilder = cb
	return a
}

// Run starts the agent loop for a given task.
func (a *Agent) Run(ctx context.Context, task string) <-chan Event {
	ch := make(chan Event, 256)
	go a.loop(ctx, task, ch)
	return ch
}

func (a *Agent) loop(ctx context.Context, task string, ch chan<- Event) {
	defer close(ch)

	// Check for embedded image data in task
	var content interface{} = task
	if strings.HasPrefix(task, "[IMAGE]") {
		// Parse image data from task
		lines := strings.Split(task, "\n")
		var base64Data, mediaType string
		var endIdx int
		
		for i, line := range lines {
			if line == "[END_IMAGE]" {
				endIdx = i
				break
			}
			if strings.HasPrefix(line, "base64:") {
				base64Data = strings.TrimPrefix(line, "base64:")
			}
			if strings.HasPrefix(line, "media_type:") {
				mediaType = strings.TrimPrefix(line, "media_type:")
			}
		}
		
		// Extract the actual task text after the image block
		var taskText string
		if endIdx > 0 && endIdx+1 < len(lines) {
			taskText = strings.TrimSpace(strings.Join(lines[endIdx+1:], "\n"))
		}
		if taskText == "" {
			taskText = "analyze this image"
		}
		
		// Build content blocks: [image, text]
		content = []interface{}{
			providers.ImageContent{
				Type: "image",
				Source: providers.ImageSource{
					Type:      "base64",
					MediaType: mediaType,
					Data:      base64Data,
				},
			},
			map[string]interface{}{
				"type": "text",
				"text": taskText,
			},
		}
		task = taskText // Update task for context builder
	}

	// pre-inject codebase context locally (zero LLM round trips)
	if a.contextBuilder != nil {
		if preCtx := a.contextBuilder(task); preCtx != "" {
			contextBlock := "<context>\n" +
				"The following is pre-loaded codebase context relevant to the task. " +
				"It is complete — do NOT call codebase_context or codebase_search unless you need something specific not shown here.\n\n" +
				preCtx +
				"</context>"
			
			// If we have image content, append context as text block
			if contentBlocks, ok := content.([]interface{}); ok {
				contentBlocks = append(contentBlocks, map[string]interface{}{
					"type": "text",
					"text": contextBlock,
				})
				content = contentBlocks
			} else {
				// Pure text mode: build content as blocks [context, task]
				// This way context is sent to LLM but only task appears in stream
				content = []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": contextBlock,
					},
					map[string]interface{}{
						"type": "text",
						"text": task,
					},
				}
			}
		}
	}

	// build messages: history + new user turn
	messages := make([]providers.Message, len(a.history), len(a.history)+1)
	copy(messages, a.history)
	messages = append(messages, providers.Message{Role: "user", Content: content})

	for round := 0; round < a.maxRounds; round++ {
		events, err := a.provider.Stream(ctx, a.model, messages, a.tools.Definitions(), a.system)
		if err != nil {
			ch <- Event{Type: EventError, AgentID: a.ID, Error: err}
			return
		}

		var textBuf strings.Builder
		var toolCalls []providers.ToolUseCall

		for ev := range events {
			switch ev.Type {
			case providers.EventDelta:
				textBuf.WriteString(ev.Text)
				ch <- Event{Type: EventToken, AgentID: a.ID, Text: ev.Text}
			case providers.EventToolCall:
				toolCalls = append(toolCalls, *ev.ToolUse)
			case providers.EventError:
				ch <- Event{Type: EventError, AgentID: a.ID, Error: ev.Error}
				return
			}
		}

		assistantContent := a.buildAssistantContent(textBuf.String(), toolCalls)
		messages = append(messages, providers.Message{Role: "assistant", Content: assistantContent})

		if len(toolCalls) == 0 {
			// persist full conversation for next turn
			a.history = messages
			ch <- Event{Type: EventDone, AgentID: a.ID}
			return
		}

		// execute all tool calls from this turn in parallel
		toolResults := a.executeParallel(ctx, toolCalls, ch)
		if toolResults == nil {
			return // context cancelled
		}

		messages = append(messages, providers.Message{Role: "user", Content: toolResults})
	}
}

// executeParallel runs all tool calls concurrently and returns results in
// original order. Returns nil if ctx is cancelled.
func (a *Agent) executeParallel(ctx context.Context, calls []providers.ToolUseCall, ch chan<- Event) []interface{} {
	type result struct {
		idx       int
		id        string
		output    string // full output sent to UI
		llmOutput string // compact version sent back to LLM
	}

	results := make([]result, len(calls))
	var wg sync.WaitGroup
	var mu sync.Mutex
	cancelled := false

	for i, tc := range calls {
		wg.Add(1)
		go func(idx int, tc providers.ToolUseCall) {
			defer wg.Done()

			argsJSON, _ := json.Marshal(tc.Input)
			ch <- Event{Type: EventToolCall, AgentID: a.ID, Action: fmt.Sprintf("%s(%s)", tc.Name, argsJSON)}

			output, err := a.tools.Execute(a.workdir, tc.Name, tc.Input)
			if err != nil {
				output = fmt.Sprintf("error: %v", err)
			}

			// UI gets full output (diff rendering); LLM gets compact summary
			llmOutput := output
			if strings.HasPrefix(output, "DIFF:") {
				// "DIFF:path/to/file\n+lines..." → "updated path/to/file"
				firstLine := output
				if nl := strings.IndexByte(output, '\n'); nl != -1 {
					firstLine = output[:nl]
				}
				llmOutput = "updated " + strings.TrimPrefix(firstLine, "DIFF:")
			}

			ch <- Event{Type: EventToolResult, AgentID: a.ID, Result: output}

			mu.Lock()
			results[idx] = result{idx: idx, id: tc.ID, output: output, llmOutput: llmOutput}
			mu.Unlock()
		}(i, tc)
	}

	// wait or cancel
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-ctx.Done():
		mu.Lock()
		cancelled = true
		mu.Unlock()
		<-done
		ch <- Event{Type: EventError, AgentID: a.ID, Error: ctx.Err()}
		return nil
	case <-done:
	}

	if cancelled {
		return nil
	}

	// build tool_result blocks in original order (use compact llmOutput for history)
	out := make([]interface{}, len(results))
	for _, r := range results {
		out[r.idx] = map[string]interface{}{
			"type":        "tool_result",
			"tool_use_id": r.id,
			"content":     r.llmOutput,
		}
	}
	return out
}

func (a *Agent) buildAssistantContent(text string, toolCalls []providers.ToolUseCall) interface{} {
	if len(toolCalls) == 0 {
		return text
	}
	var blocks []interface{}
	if text != "" {
		blocks = append(blocks, map[string]interface{}{"type": "text", "text": text})
	}
	for _, tc := range toolCalls {
		blocks = append(blocks, map[string]interface{}{
			"type":  "tool_use",
			"id":    tc.ID,
			"name":  tc.Name,
			"input": tc.Input,
		})
	}
	return blocks
}
