package adapter

import (
	"bytes"
	"strconv"
	"strings"
	"time"
)

type anthropicTextOnlyResponse struct {
	ID           string
	Model        string
	Text         string
	StopReason   string
	InputTokens  int
	OutputTokens int
}

func translateAnthropicTextOnlyToOpenAI(data []byte) ([]byte, bool, error) {
	if bytes.Contains(data, []byte(`"tool_use"`)) || bytes.Contains(data, []byte(`"error"`)) {
		return nil, false, nil
	}

	resp, ok, err := parseAnthropicTextOnlyResponse(data)
	if !ok || err != nil {
		return nil, ok, err
	}

	finishReason := stopReasonToFinishReason(resp.StopReason)
	totalTokens := resp.InputTokens + resp.OutputTokens
	created := time.Now().Unix()

	out := make([]byte, 0, len(data)+128)
	out = append(out, `{"id":`...)
	out = strconv.AppendQuote(out, resp.ID)
	out = append(out, `,"object":"chat.completion","created":`...)
	out = strconv.AppendInt(out, created, 10)
	out = append(out, `,"model":`...)
	out = strconv.AppendQuote(out, resp.Model)
	out = append(out, `,"choices":[{"index":0,"message":{"role":"assistant","content":`...)
	out = strconv.AppendQuote(out, resp.Text)
	out = append(out, `},"finish_reason":`...)
	out = strconv.AppendQuote(out, finishReason)
	out = append(out, `}],"usage":{"prompt_tokens":`...)
	out = strconv.AppendInt(out, int64(resp.InputTokens), 10)
	out = append(out, `,"completion_tokens":`...)
	out = strconv.AppendInt(out, int64(resp.OutputTokens), 10)
	out = append(out, `,"total_tokens":`...)
	out = strconv.AppendInt(out, int64(totalTokens), 10)
	out = append(out, `}}`...)
	return out, true, nil
}

func parseAnthropicTextOnlyResponse(data []byte) (anthropicTextOnlyResponse, bool, error) {
	var resp anthropicTextOnlyResponse

	idx := skipAdapterJSONWhitespace(data, 0)
	if idx >= len(data) || data[idx] != '{' {
		return resp, false, nil
	}
	idx++

	var (
		haveID,
		haveModel,
		haveContent,
		haveUsage bool
	)

	for {
		idx = skipAdapterJSONWhitespace(data, idx)
		if idx >= len(data) {
			return resp, false, nil
		}
		if data[idx] == '}' {
			break
		}

		key, next, err := scanAdapterJSONString(data, idx)
		if err != nil {
			return resp, false, nil
		}
		idx = skipAdapterJSONWhitespace(data, next)
		if idx >= len(data) || data[idx] != ':' {
			return resp, false, nil
		}
		idx = skipAdapterJSONWhitespace(data, idx+1)

		switch key {
		case "id":
			value, next, err := scanAdapterJSONString(data, idx)
			if err != nil {
				return resp, false, nil
			}
			resp.ID = value
			haveID = true
			idx = next
		case "model":
			value, next, err := scanAdapterJSONString(data, idx)
			if err != nil {
				return resp, false, nil
			}
			resp.Model = value
			haveModel = true
			idx = next
		case "stop_reason":
			value, next, err := scanAdapterJSONStringOrNull(data, idx)
			if err != nil {
				return resp, false, nil
			}
			resp.StopReason = value
			idx = next
		case "content":
			text, next, ok, err := parseAnthropicTextContentArray(data, idx)
			if err != nil {
				return resp, false, nil
			}
			if !ok {
				return resp, false, nil
			}
			resp.Text = text
			haveContent = true
			idx = next
		case "usage":
			input, output, next, ok, err := parseAnthropicUsageObject(data, idx)
			if err != nil {
				return resp, false, nil
			}
			if !ok {
				return resp, false, nil
			}
			resp.InputTokens = input
			resp.OutputTokens = output
			haveUsage = true
			idx = next
		case "error":
			return resp, false, nil
		default:
			next, err := skipAdapterJSONValue(data, idx)
			if err != nil {
				return resp, false, nil
			}
			idx = next
		}

		idx = skipAdapterJSONWhitespace(data, idx)
		if idx >= len(data) {
			return resp, false, nil
		}
		if data[idx] == '}' {
			break
		}
		if data[idx] != ',' {
			return resp, false, nil
		}
		idx++
	}

	if !haveID || !haveModel || !haveContent || !haveUsage {
		return resp, false, nil
	}
	return resp, true, nil
}

func parseAnthropicTextContentArray(data []byte, idx int) (string, int, bool, error) {
	if idx >= len(data) || data[idx] != '[' {
		return "", idx, false, nil
	}
	idx++

	var (
		text    string
		builder strings.Builder
		multi   bool
	)

	for {
		idx = skipAdapterJSONWhitespace(data, idx)
		if idx >= len(data) {
			return "", idx, false, nil
		}
		if data[idx] == ']' {
			if multi {
				text = builder.String()
			}
			return text, idx + 1, true, nil
		}

		blockText, next, ok, err := parseAnthropicTextBlock(data, idx)
		if err != nil {
			return "", idx, false, err
		}
		if !ok {
			return "", idx, false, nil
		}

		if !multi && text == "" {
			text = blockText
		} else {
			if !multi {
				builder.Grow(len(text) + len(blockText))
				builder.WriteString(text)
				multi = true
			}
			builder.WriteString(blockText)
		}

		idx = skipAdapterJSONWhitespace(data, next)
		if idx >= len(data) {
			return "", idx, false, nil
		}
		if data[idx] == ']' {
			if multi {
				text = builder.String()
			}
			return text, idx + 1, true, nil
		}
		if data[idx] != ',' {
			return "", idx, false, nil
		}
		idx++
	}
}

func parseAnthropicTextBlock(data []byte, idx int) (string, int, bool, error) {
	if idx >= len(data) || data[idx] != '{' {
		return "", idx, false, nil
	}
	idx++

	var (
		blockType string
		blockText string
	)

	for {
		idx = skipAdapterJSONWhitespace(data, idx)
		if idx >= len(data) {
			return "", idx, false, nil
		}
		if data[idx] == '}' {
			if blockType != "text" {
				return "", idx, false, nil
			}
			return blockText, idx + 1, true, nil
		}

		key, next, err := scanAdapterJSONString(data, idx)
		if err != nil {
			return "", idx, false, err
		}
		idx = skipAdapterJSONWhitespace(data, next)
		if idx >= len(data) || data[idx] != ':' {
			return "", idx, false, nil
		}
		idx = skipAdapterJSONWhitespace(data, idx+1)

		switch key {
		case "type":
			value, next, err := scanAdapterJSONString(data, idx)
			if err != nil {
				return "", idx, false, err
			}
			blockType = value
			idx = next
		case "text":
			value, next, err := scanAdapterJSONString(data, idx)
			if err != nil {
				return "", idx, false, err
			}
			blockText = value
			idx = next
		default:
			next, err := skipAdapterJSONValue(data, idx)
			if err != nil {
				return "", idx, false, err
			}
			idx = next
		}

		idx = skipAdapterJSONWhitespace(data, idx)
		if idx >= len(data) {
			return "", idx, false, nil
		}
		if data[idx] == '}' {
			if blockType != "text" {
				return "", idx, false, nil
			}
			return blockText, idx + 1, true, nil
		}
		if data[idx] != ',' {
			return "", idx, false, nil
		}
		idx++
	}
}

func parseAnthropicUsageObject(data []byte, idx int) (int, int, int, bool, error) {
	if idx >= len(data) || data[idx] != '{' {
		return 0, 0, idx, false, nil
	}
	idx++

	var (
		input, output         int
		haveInput, haveOutput bool
	)

	for {
		idx = skipAdapterJSONWhitespace(data, idx)
		if idx >= len(data) {
			return 0, 0, idx, false, nil
		}
		if data[idx] == '}' {
			return input, output, idx + 1, haveInput && haveOutput, nil
		}

		key, next, err := scanAdapterJSONString(data, idx)
		if err != nil {
			return 0, 0, idx, false, err
		}
		idx = skipAdapterJSONWhitespace(data, next)
		if idx >= len(data) || data[idx] != ':' {
			return 0, 0, idx, false, nil
		}
		idx = skipAdapterJSONWhitespace(data, idx+1)

		switch key {
		case "input_tokens":
			value, next, err := scanAdapterJSONInt(data, idx)
			if err != nil {
				return 0, 0, idx, false, err
			}
			input = value
			haveInput = true
			idx = next
		case "output_tokens":
			value, next, err := scanAdapterJSONInt(data, idx)
			if err != nil {
				return 0, 0, idx, false, err
			}
			output = value
			haveOutput = true
			idx = next
		default:
			next, err := skipAdapterJSONValue(data, idx)
			if err != nil {
				return 0, 0, idx, false, err
			}
			idx = next
		}

		idx = skipAdapterJSONWhitespace(data, idx)
		if idx >= len(data) {
			return 0, 0, idx, false, nil
		}
		if data[idx] == '}' {
			return input, output, idx + 1, haveInput && haveOutput, nil
		}
		if data[idx] != ',' {
			return 0, 0, idx, false, nil
		}
		idx++
	}
}

func skipAdapterJSONValue(data []byte, idx int) (int, error) {
	idx = skipAdapterJSONWhitespace(data, idx)
	if idx >= len(data) {
		return idx, strconv.ErrSyntax
	}

	switch data[idx] {
	case '{':
		return skipAdapterJSONObject(data, idx)
	case '[':
		return skipAdapterJSONArray(data, idx)
	case '"':
		_, next, err := scanAdapterJSONString(data, idx)
		return next, err
	case 't':
		return skipAdapterJSONLiteral(data, idx, "true")
	case 'f':
		return skipAdapterJSONLiteral(data, idx, "false")
	case 'n':
		return skipAdapterJSONLiteral(data, idx, "null")
	default:
		if data[idx] == '-' || isAdapterJSONDigit(data[idx]) {
			_, next, err := scanAdapterJSONInt(data, idx)
			if err == nil {
				return next, nil
			}
		}
		return idx, strconv.ErrSyntax
	}
}

func skipAdapterJSONObject(data []byte, idx int) (int, error) {
	if idx >= len(data) || data[idx] != '{' {
		return idx, strconv.ErrSyntax
	}
	idx++

	for {
		idx = skipAdapterJSONWhitespace(data, idx)
		if idx >= len(data) {
			return idx, strconv.ErrSyntax
		}
		if data[idx] == '}' {
			return idx + 1, nil
		}

		_, next, err := scanAdapterJSONString(data, idx)
		if err != nil {
			return idx, err
		}
		idx = skipAdapterJSONWhitespace(data, next)
		if idx >= len(data) || data[idx] != ':' {
			return idx, strconv.ErrSyntax
		}
		idx = skipAdapterJSONWhitespace(data, idx+1)
		next, err = skipAdapterJSONValue(data, idx)
		if err != nil {
			return idx, err
		}
		idx = skipAdapterJSONWhitespace(data, next)
		if idx >= len(data) {
			return idx, strconv.ErrSyntax
		}
		if data[idx] == '}' {
			return idx + 1, nil
		}
		if data[idx] != ',' {
			return idx, strconv.ErrSyntax
		}
		idx++
	}
}

func skipAdapterJSONArray(data []byte, idx int) (int, error) {
	if idx >= len(data) || data[idx] != '[' {
		return idx, strconv.ErrSyntax
	}
	idx++

	for {
		idx = skipAdapterJSONWhitespace(data, idx)
		if idx >= len(data) {
			return idx, strconv.ErrSyntax
		}
		if data[idx] == ']' {
			return idx + 1, nil
		}
		next, err := skipAdapterJSONValue(data, idx)
		if err != nil {
			return idx, err
		}
		idx = skipAdapterJSONWhitespace(data, next)
		if idx >= len(data) {
			return idx, strconv.ErrSyntax
		}
		if data[idx] == ']' {
			return idx + 1, nil
		}
		if data[idx] != ',' {
			return idx, strconv.ErrSyntax
		}
		idx++
	}
}

func scanAdapterJSONString(data []byte, idx int) (string, int, error) {
	if idx >= len(data) || data[idx] != '"' {
		return "", idx, strconv.ErrSyntax
	}

	start := idx
	idx++
	hasEscape := false

	for idx < len(data) {
		switch data[idx] {
		case '"':
			if !hasEscape {
				return string(data[start+1 : idx]), idx + 1, nil
			}
			value, err := strconv.Unquote(string(data[start : idx+1]))
			if err != nil {
				return "", idx, err
			}
			return value, idx + 1, nil
		case '\\':
			hasEscape = true
			idx += 2
			continue
		default:
			if data[idx] < 0x20 {
				return "", idx, strconv.ErrSyntax
			}
		}
		idx++
	}

	return "", idx, strconv.ErrSyntax
}

func scanAdapterJSONStringOrNull(data []byte, idx int) (string, int, error) {
	if idx < len(data) && data[idx] == '"' {
		return scanAdapterJSONString(data, idx)
	}
	next, err := skipAdapterJSONLiteral(data, idx, "null")
	if err != nil {
		return "", idx, err
	}
	return "", next, nil
}

func scanAdapterJSONInt(data []byte, idx int) (int, int, error) {
	start := idx
	if idx >= len(data) {
		return 0, idx, strconv.ErrSyntax
	}
	if data[idx] == '-' {
		idx++
	}
	if idx >= len(data) || !isAdapterJSONDigit(data[idx]) {
		return 0, idx, strconv.ErrSyntax
	}
	for idx < len(data) && isAdapterJSONDigit(data[idx]) {
		idx++
	}
	value, err := strconv.Atoi(string(data[start:idx]))
	if err != nil {
		return 0, idx, err
	}
	return value, idx, nil
}

func skipAdapterJSONLiteral(data []byte, idx int, literal string) (int, error) {
	if idx+len(literal) > len(data) {
		return idx, strconv.ErrSyntax
	}
	for i := 0; i < len(literal); i++ {
		if data[idx+i] != literal[i] {
			return idx, strconv.ErrSyntax
		}
	}
	return idx + len(literal), nil
}

func skipAdapterJSONWhitespace(data []byte, idx int) int {
	for idx < len(data) {
		switch data[idx] {
		case ' ', '\t', '\r', '\n':
			idx++
		default:
			return idx
		}
	}
	return idx
}

func isAdapterJSONDigit(b byte) bool {
	return b >= '0' && b <= '9'
}
