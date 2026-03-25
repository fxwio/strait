package middleware

import (
	"fmt"
	"strconv"
	"strings"
)

type requestBodyInspection struct {
	Model                        string
	Stream                       bool
	HasStreamOptions             bool
	StreamOptionsHasIncludeUsage bool
	StreamOptionsObjectStart     int
	StreamOptionsObjectEnd       int
}

func inspectRequestBody(body []byte) (requestBodyInspection, error) {
	var info requestBodyInspection
	info.StreamOptionsObjectStart = -1
	info.StreamOptionsObjectEnd = -1

	idx := skipJSONWhitespace(body, 0)
	if idx >= len(body) || body[idx] != '{' {
		return info, fmt.Errorf("request body must be a json object")
	}

	end, err := inspectTopLevelObject(body, idx, &info)
	if err != nil {
		return info, err
	}

	end = skipJSONWhitespace(body, end)
	if end != len(body) {
		return info, fmt.Errorf("unexpected trailing content")
	}

	return info, nil
}

func inspectTopLevelObject(body []byte, idx int, info *requestBodyInspection) (int, error) {
	if idx >= len(body) || body[idx] != '{' {
		return idx, fmt.Errorf("expected object")
	}
	idx++

	for {
		idx = skipJSONWhitespace(body, idx)
		if idx >= len(body) {
			return idx, fmt.Errorf("unexpected end of json object")
		}
		if body[idx] == '}' {
			return idx + 1, nil
		}

		key, next, err := scanJSONString(body, idx)
		if err != nil {
			return idx, err
		}
		idx = skipJSONWhitespace(body, next)
		if idx >= len(body) || body[idx] != ':' {
			return idx, fmt.Errorf("expected ':' after object key")
		}
		idx = skipJSONWhitespace(body, idx+1)
		if idx >= len(body) {
			return idx, fmt.Errorf("unexpected end after ':'")
		}

		switch key {
		case "model":
			model, next, err := scanJSONString(body, idx)
			if err != nil {
				return idx, fmt.Errorf("model must be a string: %w", err)
			}
			info.Model = strings.TrimSpace(model)
			idx = next
		case "stream":
			stream, next, err := scanJSONBool(body, idx)
			if err != nil {
				return idx, fmt.Errorf("stream must be a boolean: %w", err)
			}
			info.Stream = stream
			idx = next
		case "messages", "tools":
			if body[idx] != '[' {
				return idx, fmt.Errorf("%s must be an array", key)
			}
			next, err := skipJSONValue(body, idx)
			if err != nil {
				return idx, err
			}
			idx = next
		case "response_format":
			if body[idx] != '{' {
				return idx, fmt.Errorf("response_format must be an object")
			}
			next, err := skipJSONValue(body, idx)
			if err != nil {
				return idx, err
			}
			idx = next
		case "stream_options":
			if body[idx] != '{' {
				return idx, fmt.Errorf("stream_options must be an object")
			}
			info.HasStreamOptions = true
			info.StreamOptionsObjectStart = idx
			foundIncludeUsage, next, err := inspectObjectForKey(body, idx, "include_usage")
			if err != nil {
				return idx, err
			}
			info.StreamOptionsHasIncludeUsage = foundIncludeUsage
			info.StreamOptionsObjectEnd = next
			idx = next
		default:
			next, err := skipJSONValue(body, idx)
			if err != nil {
				return idx, err
			}
			idx = next
		}

		idx = skipJSONWhitespace(body, idx)
		if idx >= len(body) {
			return idx, fmt.Errorf("unexpected end after object value")
		}
		switch body[idx] {
		case ',':
			idx++
		case '}':
			return idx + 1, nil
		default:
			return idx, fmt.Errorf("expected ',' or '}' after object value")
		}
	}
}

func inspectObjectForKey(body []byte, idx int, targetKey string) (bool, int, error) {
	if idx >= len(body) || body[idx] != '{' {
		return false, idx, fmt.Errorf("expected object")
	}
	idx++
	found := false

	for {
		idx = skipJSONWhitespace(body, idx)
		if idx >= len(body) {
			return found, idx, fmt.Errorf("unexpected end of json object")
		}
		if body[idx] == '}' {
			return found, idx + 1, nil
		}

		key, next, err := scanJSONString(body, idx)
		if err != nil {
			return found, idx, err
		}
		if key == targetKey {
			found = true
		}

		idx = skipJSONWhitespace(body, next)
		if idx >= len(body) || body[idx] != ':' {
			return found, idx, fmt.Errorf("expected ':' after object key")
		}
		idx = skipJSONWhitespace(body, idx+1)

		next, err = skipJSONValue(body, idx)
		if err != nil {
			return found, idx, err
		}
		idx = skipJSONWhitespace(body, next)

		if idx >= len(body) {
			return found, idx, fmt.Errorf("unexpected end after object value")
		}
		switch body[idx] {
		case ',':
			idx++
		case '}':
			return found, idx + 1, nil
		default:
			return found, idx, fmt.Errorf("expected ',' or '}' after object value")
		}
	}
}

func skipJSONValue(body []byte, idx int) (int, error) {
	idx = skipJSONWhitespace(body, idx)
	if idx >= len(body) {
		return idx, fmt.Errorf("unexpected end of json value")
	}

	switch body[idx] {
	case '{':
		return skipJSONObject(body, idx)
	case '[':
		return skipJSONArray(body, idx)
	case '"':
		_, next, err := scanJSONString(body, idx)
		return next, err
	case 't':
		return skipJSONLiteral(body, idx, "true")
	case 'f':
		return skipJSONLiteral(body, idx, "false")
	case 'n':
		return skipJSONLiteral(body, idx, "null")
	default:
		if body[idx] == '-' || isJSONDigit(body[idx]) {
			return skipJSONNumber(body, idx)
		}
		return idx, fmt.Errorf("invalid json value")
	}
}

func skipJSONObject(body []byte, idx int) (int, error) {
	if idx >= len(body) || body[idx] != '{' {
		return idx, fmt.Errorf("expected object")
	}
	idx++

	for {
		idx = skipJSONWhitespace(body, idx)
		if idx >= len(body) {
			return idx, fmt.Errorf("unexpected end of object")
		}
		if body[idx] == '}' {
			return idx + 1, nil
		}

		_, next, err := scanJSONString(body, idx)
		if err != nil {
			return idx, err
		}
		idx = skipJSONWhitespace(body, next)
		if idx >= len(body) || body[idx] != ':' {
			return idx, fmt.Errorf("expected ':' after object key")
		}
		idx = skipJSONWhitespace(body, idx+1)

		next, err = skipJSONValue(body, idx)
		if err != nil {
			return idx, err
		}
		idx = skipJSONWhitespace(body, next)

		if idx >= len(body) {
			return idx, fmt.Errorf("unexpected end after object value")
		}
		switch body[idx] {
		case ',':
			idx++
		case '}':
			return idx + 1, nil
		default:
			return idx, fmt.Errorf("expected ',' or '}' after object value")
		}
	}
}

func skipJSONArray(body []byte, idx int) (int, error) {
	if idx >= len(body) || body[idx] != '[' {
		return idx, fmt.Errorf("expected array")
	}
	idx++

	for {
		idx = skipJSONWhitespace(body, idx)
		if idx >= len(body) {
			return idx, fmt.Errorf("unexpected end of array")
		}
		if body[idx] == ']' {
			return idx + 1, nil
		}

		next, err := skipJSONValue(body, idx)
		if err != nil {
			return idx, err
		}
		idx = skipJSONWhitespace(body, next)

		if idx >= len(body) {
			return idx, fmt.Errorf("unexpected end after array value")
		}
		switch body[idx] {
		case ',':
			idx++
		case ']':
			return idx + 1, nil
		default:
			return idx, fmt.Errorf("expected ',' or ']' after array value")
		}
	}
}

func scanJSONString(body []byte, idx int) (string, int, error) {
	if idx >= len(body) || body[idx] != '"' {
		return "", idx, fmt.Errorf("expected string")
	}

	start := idx
	idx++
	hasEscape := false

	for idx < len(body) {
		switch body[idx] {
		case '"':
			if !hasEscape {
				return string(body[start+1 : idx]), idx + 1, nil
			}

			unquoted, err := strconv.Unquote(string(body[start : idx+1]))
			if err != nil {
				return "", idx, err
			}
			return unquoted, idx + 1, nil
		case '\\':
			hasEscape = true
			idx += 2
			continue
		default:
			if body[idx] < 0x20 {
				return "", idx, fmt.Errorf("invalid control character in string")
			}
		}
		idx++
	}

	return "", idx, fmt.Errorf("unterminated string")
}

func scanJSONBool(body []byte, idx int) (bool, int, error) {
	if next, err := skipJSONLiteral(body, idx, "true"); err == nil {
		return true, next, nil
	}
	if next, err := skipJSONLiteral(body, idx, "false"); err == nil {
		return false, next, nil
	}
	return false, idx, fmt.Errorf("expected boolean")
}

func skipJSONLiteral(body []byte, idx int, literal string) (int, error) {
	if idx+len(literal) > len(body) {
		return idx, fmt.Errorf("unexpected end of literal")
	}
	for i := 0; i < len(literal); i++ {
		if body[idx+i] != literal[i] {
			return idx, fmt.Errorf("expected literal %s", literal)
		}
	}
	return idx + len(literal), nil
}

func skipJSONNumber(body []byte, idx int) (int, error) {
	start := idx
	if body[idx] == '-' {
		idx++
		if idx >= len(body) {
			return idx, fmt.Errorf("invalid number")
		}
	}

	if body[idx] == '0' {
		idx++
	} else {
		if !isJSONDigit(body[idx]) {
			return idx, fmt.Errorf("invalid number")
		}
		for idx < len(body) && isJSONDigit(body[idx]) {
			idx++
		}
	}

	if idx < len(body) && body[idx] == '.' {
		idx++
		if idx >= len(body) || !isJSONDigit(body[idx]) {
			return idx, fmt.Errorf("invalid fractional number")
		}
		for idx < len(body) && isJSONDigit(body[idx]) {
			idx++
		}
	}

	if idx < len(body) && (body[idx] == 'e' || body[idx] == 'E') {
		idx++
		if idx < len(body) && (body[idx] == '+' || body[idx] == '-') {
			idx++
		}
		if idx >= len(body) || !isJSONDigit(body[idx]) {
			return idx, fmt.Errorf("invalid exponent")
		}
		for idx < len(body) && isJSONDigit(body[idx]) {
			idx++
		}
	}

	if idx == start {
		return idx, fmt.Errorf("invalid number")
	}
	return idx, nil
}

func skipJSONWhitespace(body []byte, idx int) int {
	for idx < len(body) {
		switch body[idx] {
		case ' ', '\t', '\r', '\n':
			idx++
		default:
			return idx
		}
	}
	return idx
}

func isJSONDigit(b byte) bool {
	return b >= '0' && b <= '9'
}
