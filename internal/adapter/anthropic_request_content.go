package adapter

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/fxwio/strait/internal/model"
)

type openAIMessageContentKind uint8

const (
	openAIMessageContentString openAIMessageContentKind = iota
	openAIMessageContentParts
)

type openAIMessageContent struct {
	kind  openAIMessageContentKind
	text  string
	parts []model.ContentPart
}

func decodeOpenAIMessageContent(raw json.RawMessage) (openAIMessageContent, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return openAIMessageContent{kind: openAIMessageContentString}, true
	}

	switch trimmed[0] {
	case '"':
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return openAIMessageContent{}, false
		}
		return openAIMessageContent{
			kind: openAIMessageContentString,
			text: text,
		}, true
	case '[':
		var parts []model.ContentPart
		if err := json.Unmarshal(trimmed, &parts); err != nil {
			return openAIMessageContent{}, false
		}
		return openAIMessageContent{
			kind:  openAIMessageContentParts,
			parts: parts,
		}, true
	default:
		return openAIMessageContent{}, false
	}
}

func (c openAIMessageContent) textOnly() string {
	if c.kind == openAIMessageContentString {
		return c.text
	}
	if len(c.parts) == 0 {
		return ""
	}

	var total int
	for _, part := range c.parts {
		if part.Type == "text" {
			total += len(part.Text)
		}
	}
	if total == 0 {
		return ""
	}

	var builder strings.Builder
	builder.Grow(total)
	for _, part := range c.parts {
		if part.Type == "text" {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func (c openAIMessageContent) anthropicBlocks() []AnthropicContentBlock {
	if c.kind != openAIMessageContentParts {
		return nil
	}

	blocks := make([]AnthropicContentBlock, 0, len(c.parts))
	for _, part := range c.parts {
		switch part.Type {
		case "text":
			blocks = append(blocks, AnthropicContentBlock{
				Type: "text",
				Text: part.Text,
			})
		case "image_url":
			if part.ImageURL != nil {
				blocks = append(blocks, AnthropicContentBlock{
					Type: "image",
					Source: &AnthropicImageSource{
						Type: "url",
						URL:  part.ImageURL.URL,
					},
				})
			}
		}
	}
	return blocks
}

func toolCallArgumentsAsInput(arguments string) any {
	raw := json.RawMessage(arguments)
	if json.Valid(raw) {
		return raw
	}
	return arguments
}
