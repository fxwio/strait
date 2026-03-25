package adapter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"
)

var (
	sseEventPrefix     = []byte("event:")
	sseDataPrefix      = []byte("data:")
	sseDonePayload     = []byte("[DONE]")
	sseDoneChunk       = []byte("data: [DONE]\n\n")
	assistantRoleDelta = []byte(`{"role":"assistant","content":""}`)
)

func TranslateAnthropicStream(resp *http.Response, w http.ResponseWriter, fallbackModel string) error {
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Del("Server")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)
	created := time.Now().Unix()

	state := anthropicStreamState{
		w:        w,
		flusher:  flusher,
		canFlush: canFlush,
		created:  created,
		msgID:    "chatcmpl-anthropic-" + strconv.FormatInt(created, 10),
		model:    fallbackModel,
	}

	reader := bufio.NewReader(resp.Body)
	var currentEvent anthropicSSEEvent
	var lineBuf []byte

	for {
		line, err := readSSELine(reader, lineBuf[:0])
		lineBuf = line
		if err != nil && err != io.EOF {
			return err
		}

		if len(line) == 0 {
			if currentEvent.hasData() {
				if bytes.Equal(currentEvent.data, sseDonePayload) {
					goto done
				}
				if err := dispatchAnthropicSSEEvent(&currentEvent, &state); err != nil {
					return err
				}
			}
			currentEvent.reset()
		} else {
			currentEvent.consume(line)
		}

		if err == io.EOF {
			break
		}
	}

done:
	return state.writeDone()
}

type anthropicStreamState struct {
	w        http.ResponseWriter
	flusher  http.Flusher
	canFlush bool
	created  int64
	msgID    string
	model    string
	chunkBuf []byte
	deltaBuf []byte
}

func (s *anthropicStreamState) writeChunk(deltaJSON []byte, finishReason *string) error {
	buf := s.chunkBuf[:0]
	buf = append(buf, "data: {"...)
	buf = appendJSONStringField(buf, "id", s.msgID)
	buf = append(buf, ',')
	buf = appendJSONConstStringField(buf, "object", "chat.completion.chunk")
	buf = append(buf, ',')
	buf = appendJSONIntField(buf, "created", s.created)
	buf = append(buf, ',')
	buf = appendJSONStringField(buf, "model", s.model)
	buf = append(buf, `,"choices":[{"index":0,"delta":`...)
	buf = append(buf, deltaJSON...)
	buf = append(buf, `,"finish_reason":`...)
	if finishReason != nil {
		buf = strconv.AppendQuote(buf, *finishReason)
	} else {
		buf = append(buf, "null"...)
	}
	buf = append(buf, "}]}\n\n"...)

	s.chunkBuf = buf
	if _, err := s.w.Write(buf); err != nil {
		return err
	}
	if s.canFlush {
		s.flusher.Flush()
	}
	return nil
}

func (s *anthropicStreamState) writeDone() error {
	if _, err := s.w.Write(sseDoneChunk); err != nil {
		return err
	}
	if s.canFlush {
		s.flusher.Flush()
	}
	return nil
}

func (s *anthropicStreamState) textDelta(text string) []byte {
	buf := s.deltaBuf[:0]
	buf = append(buf, `{"content":`...)
	buf = strconv.AppendQuote(buf, text)
	buf = append(buf, '}')
	s.deltaBuf = buf
	return buf
}

func (s *anthropicStreamState) toolHeaderDelta(index int, toolID, toolName string) []byte {
	buf := s.deltaBuf[:0]
	buf = append(buf, `{"tool_calls":[{"index":`...)
	buf = strconv.AppendInt(buf, int64(index), 10)
	buf = append(buf, `,"id":`...)
	buf = strconv.AppendQuote(buf, toolID)
	buf = append(buf, `,"type":"function","function":{"name":`...)
	buf = strconv.AppendQuote(buf, toolName)
	buf = append(buf, `,"arguments":""}}]}`...)
	s.deltaBuf = buf
	return buf
}

func (s *anthropicStreamState) toolArgumentDelta(index int, partialJSON string) []byte {
	buf := s.deltaBuf[:0]
	buf = append(buf, `{"tool_calls":[{"index":`...)
	buf = strconv.AppendInt(buf, int64(index), 10)
	buf = append(buf, `,"function":{"arguments":`...)
	buf = strconv.AppendQuote(buf, partialJSON)
	buf = append(buf, `}}]}`...)
	s.deltaBuf = buf
	return buf
}

type anthropicSSEEvent struct {
	eventType string
	data      []byte
}

func (e *anthropicSSEEvent) consume(line []byte) {
	switch {
	case bytes.HasPrefix(line, sseEventPrefix):
		e.eventType = string(bytes.TrimSpace(line[len(sseEventPrefix):]))
	case bytes.HasPrefix(line, sseDataPrefix):
		payload := bytes.TrimSpace(line[len(sseDataPrefix):])
		if len(payload) == 0 {
			return
		}
		if len(e.data) > 0 {
			e.data = append(e.data, '\n')
		}
		e.data = append(e.data, payload...)
	}
}

func (e *anthropicSSEEvent) hasData() bool {
	return len(e.data) > 0
}

func (e *anthropicSSEEvent) reset() {
	e.eventType = ""
	e.data = e.data[:0]
}

func readSSELine(reader *bufio.Reader, dst []byte) ([]byte, error) {
	dst = dst[:0]
	for {
		chunk, err := reader.ReadSlice('\n')
		dst = append(dst, chunk...)

		switch err {
		case nil:
			return bytes.TrimRight(dst, "\r\n"), nil
		case bufio.ErrBufferFull:
			continue
		case io.EOF:
			return bytes.TrimRight(dst, "\r\n"), io.EOF
		default:
			return nil, err
		}
	}
}

func dispatchAnthropicSSEEvent(event *anthropicSSEEvent, state *anthropicStreamState) error {
	evType := event.eventType
	if evType == "" {
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(event.data, &envelope); err != nil {
			return nil
		}
		evType = envelope.Type
	}
	if evType == "" {
		return nil
	}
	return dispatchAnthropicEvent(evType, event.data, state)
}

func dispatchAnthropicEvent(evType string, data []byte, state *anthropicStreamState) error {
	switch evType {
	case "message_start":
		var payload struct {
			Message struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"message"`
		}
		if err := json.Unmarshal(data, &payload); err == nil {
			if payload.Message.ID != "" {
				state.msgID = payload.Message.ID
			}
			if payload.Message.Model != "" {
				state.model = payload.Message.Model
			}
		}
		return state.writeChunk(assistantRoleDelta, nil)

	case "content_block_start":
		var payload struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil
		}
		if payload.ContentBlock.Type == "tool_use" {
			return state.writeChunk(state.toolHeaderDelta(payload.Index, payload.ContentBlock.ID, payload.ContentBlock.Name), nil)
		}
		return nil

	case "content_block_delta":
		var payload struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil
		}
		switch payload.Delta.Type {
		case "text_delta":
			return state.writeChunk(state.textDelta(payload.Delta.Text), nil)
		case "input_json_delta":
			return state.writeChunk(state.toolArgumentDelta(payload.Index, payload.Delta.PartialJSON), nil)
		}

	case "message_delta":
		var payload struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
		}
		_ = json.Unmarshal(data, &payload)
		finishReason := stopReasonToFinishReason(payload.Delta.StopReason)
		return state.writeChunk([]byte(`{}`), &finishReason)
	}

	return nil
}

func appendJSONStringField(dst []byte, key, value string) []byte {
	dst = append(dst, '"')
	dst = append(dst, key...)
	dst = append(dst, `":`...)
	return strconv.AppendQuote(dst, value)
}

func appendJSONConstStringField(dst []byte, key, value string) []byte {
	dst = append(dst, '"')
	dst = append(dst, key...)
	dst = append(dst, `":"`...)
	dst = append(dst, value...)
	return append(dst, '"')
}

func appendJSONIntField(dst []byte, key string, value int64) []byte {
	dst = append(dst, '"')
	dst = append(dst, key...)
	dst = append(dst, `":`...)
	return strconv.AppendInt(dst, value, 10)
}
