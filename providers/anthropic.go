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

const anthropicAPI = "https://api.anthropic.com/v1/messages"

type Anthropic struct {
	APIKey string
	client *http.Client
}

func NewAnthropic(apiKey string) *Anthropic {
	return &Anthropic{
		APIKey: apiKey,
		client: &http.Client{},
	}
}

func (a *Anthropic) Stream(ctx context.Context, model string, messages []Message, tools []Tool, systemPrompt string) (<-chan Event, error) {
	body := map[string]interface{}{
		"model":      model,
		"max_tokens": 8096,
		"stream":     true,
		"messages":   a.convertMessages(messages),
	}
	if systemPrompt != "" {
		body["system"] = systemPrompt
	}
	if len(tools) > 0 {
		body["tools"] = a.convertTools(tools)
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", anthropicAPI, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", a.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("anthropic: HTTP %d", resp.StatusCode)
	}

	ch := make(chan Event, 64)
	go a.parseSSE(resp, ch)
	return ch, nil
}

func (a *Anthropic) parseSSE(resp *http.Response, ch chan<- Event) {
	defer resp.Body.Close()
	defer close(ch)

	// accumulate tool input JSON per tool_use block
	type toolBlock struct {
		id    string
		name  string
		input strings.Builder
	}
	blocks := map[int]*toolBlock{}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		raw := line[6:]
		if raw == "[DONE]" {
			ch <- Event{Type: EventDone}
			return
		}

		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			continue
		}

		evType, _ := ev["type"].(string)
		switch evType {
		case "content_block_start":
			idx := int(ev["index"].(float64))
			block, _ := ev["content_block"].(map[string]interface{})
			if block["type"] == "tool_use" {
				blocks[idx] = &toolBlock{
					id:   block["id"].(string),
					name: block["name"].(string),
				}
			}

		case "content_block_delta":
			idx := int(ev["index"].(float64))
			delta, _ := ev["delta"].(map[string]interface{})
			deltaType, _ := delta["type"].(string)

			switch deltaType {
			case "text_delta":
				text, _ := delta["text"].(string)
				ch <- Event{Type: EventDelta, Text: text}

			case "input_json_delta":
				partial, _ := delta["partial_json"].(string)
				if b, ok := blocks[idx]; ok {
					b.input.WriteString(partial)
				}
			}

		case "content_block_stop":
			idx := int(ev["index"].(float64))
			if b, ok := blocks[idx]; ok {
				var input map[string]interface{}
				json.Unmarshal([]byte(b.input.String()), &input)
				ch <- Event{
					Type: EventToolCall,
					ToolUse: &ToolUseCall{
						ID:    b.id,
						Name:  b.name,
						Input: input,
					},
				}
				delete(blocks, idx)
			}

		case "message_stop":
			ch <- Event{Type: EventDone}
			return

		case "error":
			errBlock, _ := ev["error"].(map[string]interface{})
			msg, _ := errBlock["message"].(string)
			ch <- Event{Type: EventError, Error: fmt.Errorf("anthropic: %s", msg)}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- Event{Type: EventError, Error: err}
	}
}

func (a *Anthropic) convertMessages(msgs []Message) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(msgs))
	for _, m := range msgs {
		// Handle content blocks (for image support)
		var content interface{} = m.Content
		if blocks, ok := m.Content.([]interface{}); ok {
			// Convert content blocks to Anthropic format
			convertedBlocks := make([]map[string]interface{}, 0, len(blocks))
			for _, block := range blocks {
				switch b := block.(type) {
				case ImageContent:
					convertedBlocks = append(convertedBlocks, map[string]interface{}{
						"type": "image",
						"source": map[string]interface{}{
							"type":       b.Source.Type,
							"media_type": b.Source.MediaType,
							"data":       b.Source.Data,
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

func (a *Anthropic) convertTools(tools []Tool) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]interface{}{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		})
	}
	return out
}
