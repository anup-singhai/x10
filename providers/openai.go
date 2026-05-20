package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const openaiAPI = "https://api.openai.com/v1/chat/completions"

type OpenAI struct {
	APIKey string
	client *http.Client
}

func NewOpenAI(apiKey string) *OpenAI {
	return &OpenAI{
		APIKey: apiKey,
		client: &http.Client{},
	}
}

func (o *OpenAI) Stream(ctx context.Context, model string, messages []Message, tools []Tool, systemPrompt string) (<-chan Event, error) {
	oaiMsgs := o.convertMessages(messages, systemPrompt)

	body := map[string]interface{}{
		"model":    model,
		"stream":   true,
		"messages": oaiMsgs,
	}
	if len(tools) > 0 {
		body["tools"] = o.convertTools(tools)
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", openaiAPI, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("openai: HTTP %d", resp.StatusCode)
	}

	ch := make(chan Event, 64)
	go o.parseSSE(resp, ch)
	return ch, nil
}

func (o *OpenAI) parseSSE(resp *http.Response, ch chan<- Event) {
	defer resp.Body.Close()
	defer close(ch)

	// accumulate tool call deltas: index → partial call
	type tcAcc struct {
		id    string
		name  string
		input strings.Builder
	}
	calls := map[int]*tcAcc{}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		raw := line[6:]
		if raw == "[DONE]" {
			// flush accumulated tool calls
			for _, tc := range calls {
				var input map[string]interface{}
				json.Unmarshal([]byte(tc.input.String()), &input)
				ch <- Event{
					Type: EventToolCall,
					ToolUse: &ToolUseCall{
						ID:    tc.id,
						Name:  tc.name,
						Input: input,
					},
				}
			}
			ch <- Event{Type: EventDone}
			return
		}

		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			continue
		}

		choices, _ := ev["choices"].([]interface{})
		if len(choices) == 0 {
			continue
		}
		choice := choices[0].(map[string]interface{})
		delta, _ := choice["delta"].(map[string]interface{})

		// text delta
		if content, ok := delta["content"].(string); ok && content != "" {
			ch <- Event{Type: EventDelta, Text: content}
		}

		// tool call delta
		if tcs, ok := delta["tool_calls"].([]interface{}); ok {
			for _, tc := range tcs {
				tcMap := tc.(map[string]interface{})
				idx := int(tcMap["index"].(float64))

				if _, exists := calls[idx]; !exists {
					calls[idx] = &tcAcc{}
				}
				acc := calls[idx]

				if id, ok := tcMap["id"].(string); ok && id != "" {
					acc.id = id
				}
				if fn, ok := tcMap["function"].(map[string]interface{}); ok {
					if name, ok := fn["name"].(string); ok && name != "" {
						acc.name = name
					}
					if args, ok := fn["arguments"].(string); ok {
						acc.input.WriteString(args)
					}
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- Event{Type: EventError, Error: err}
	}
}

func (o *OpenAI) convertMessages(msgs []Message, systemPrompt string) []map[string]interface{} {
	var out []map[string]interface{}
	if systemPrompt != "" {
		out = append(out, map[string]interface{}{
			"role":    "system",
			"content": systemPrompt,
		})
	}
	for _, m := range msgs {
		// Handle content blocks (for image support)
		var content interface{} = m.Content
		if blocks, ok := m.Content.([]interface{}); ok {
			// Convert content blocks to OpenAI format
			convertedBlocks := make([]map[string]interface{}, 0, len(blocks))
			for _, block := range blocks {
				switch b := block.(type) {
				case ImageContent:
					convertedBlocks = append(convertedBlocks, map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": "data:" + b.Source.MediaType + ";base64," + b.Source.Data,
						},
					})
				case map[string]interface{}:
					convertedBlocks = append(convertedBlocks, b)
				case string:
					convertedBlocks = append(convertedBlocks, map[string]interface{}{
						"type": "text",
						"text": b,
					})
				}
			}
			content = convertedBlocks
		}
		
		out = append(out, map[string]interface{}{
			"role":    m.Role,
			"content": content,
		})
	}
	return out
}

func (o *OpenAI) convertTools(tools []Tool) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		})
	}
	return out
}
