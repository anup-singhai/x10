package providers

import "context"

// Message is a single conversation turn.
type Message struct {
	Role    string      `json:"role"` // "user" | "assistant" | "tool"
	Content interface{} `json:"content"`
}

// TextContent is a plain text message content block.
type TextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ImageContent is an image message content block (base64 encoded).
type ImageContent struct {
	Type      string `json:"type"` // "image"
	Source    ImageSource `json:"source"`
}

// ImageSource contains the base64 image data and media type.
type ImageSource struct {
	Type      string `json:"type"` // "base64"
	MediaType string `json:"media_type"` // "image/jpeg", "image/png", etc.
	Data      string `json:"data"` // base64 encoded image
}

// ToolUseContent is a tool call block in the assistant message.
type ToolUseContent struct {
	Type  string      `json:"type"`
	ID    string      `json:"id"`
	Name  string      `json:"name"`
	Input interface{} `json:"input"`
}

// ToolResultContent is the result of a tool call (sent back as user message).
type ToolResultContent struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

// Tool is a tool definition passed to the model.
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

// Event is a single streaming event from the provider.
type Event struct {
	Type    EventType
	Text    string       // for Delta events
	ToolUse *ToolUseCall // for ToolCall events
	Error   error        // for Error events
}

type EventType int

const (
	EventDelta    EventType = iota // partial text token
	EventToolCall                  // model wants to call a tool
	EventDone                      // stream complete
	EventError                     // fatal error
)

// ToolUseCall is a complete tool call from the model.
type ToolUseCall struct {
	ID    string
	Name  string
	Input map[string]interface{}
}

// Provider streams LLM responses.
type Provider interface {
	Stream(ctx context.Context, model string, messages []Message, tools []Tool, systemPrompt string) (<-chan Event, error)
}
