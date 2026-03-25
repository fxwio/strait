package middleware

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/fxwio/strait/internal/response"
)

const (
	BodyContextKey             contextKey = "body_ctx"
	DefaultMaxRequestBodyBytes int64      = 4 << 20 // 4 MiB
)

// RequestBodyContext 保存请求体的两个视图：
// 1. RawBody: 客户端原始请求体，供路由、缓存等逻辑使用
// 2. UpstreamBody: 发往上游 provider 的请求体，允许在网关侧做受控增强
// 3. RequestedModel / IsStream / DecodeError: 一次解析出的轻量元信息，供后续中间件复用
type RequestBodyContext struct {
	RawBody               []byte
	UpstreamBody          []byte
	RequestedModel        string
	IsStream              bool
	StreamOptionsInjected bool
	DecodeError           error
}

// BodyContextMiddleware 统一完成四件事：
// 1. 对请求体做大小限制
// 2. 一次性读取 body
// 3. 把原始 body / upstream body 放进 context，供后续中间件复用
// 4. 提前完成 stream_options.include_usage 注入，避免 proxy 再次读取 body
func BodyContextMiddleware(maxBodyBytes int64, next http.Handler) http.Handler {
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultMaxRequestBodyBytes
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil {
			response.WriteOpenAIError(
				w,
				http.StatusBadRequest,
				"Request body is required.",
				"invalid_request_error",
				nil,
				response.Ptr("missing_request_body"),
			)
			return
		}

		limitedBody := http.MaxBytesReader(w, r.Body, maxBodyBytes)
		defer func() {
			_ = limitedBody.Close()
		}()

		rawBody, err := io.ReadAll(limitedBody)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				response.WriteOpenAIError(
					w,
					http.StatusRequestEntityTooLarge,
					fmt.Sprintf("Request body too large. Max allowed size is %d bytes.", maxBodyBytes),
					"invalid_request_error",
					nil,
					response.Ptr("request_body_too_large"),
				)
				return
			}

			response.WriteOpenAIError(
				w,
				http.StatusBadRequest,
				"Failed to read request body.",
				"invalid_request_error",
				nil,
				response.Ptr("request_body_read_failed"),
			)
			return
		}

		inspection, decodeErr := inspectRequestBody(rawBody)
		upstreamBody := rawBody
		injected := false
		if decodeErr == nil {
			upstreamBody, injected = buildUpstreamBodyFromInspection(rawBody, inspection)
		} else {
			inspection = requestBodyInspection{
				StreamOptionsObjectStart: -1,
				StreamOptionsObjectEnd:   -1,
			}
		}

		bodyCtx := RequestBodyContext{
			RawBody:               rawBody,
			UpstreamBody:          upstreamBody,
			RequestedModel:        inspection.Model,
			IsStream:              inspection.Stream,
			StreamOptionsInjected: injected,
			DecodeError:           decodeErr,
		}

		applyRequestBody(r, upstreamBody)

		r = putRequestBodyContext(r, bodyCtx)
		next.ServeHTTP(w, r)
	})
}

func GetRequestBodyContext(r *http.Request) (*RequestBodyContext, bool) {
	if r == nil {
		return nil, false
	}
	if state, ok := getRequestState(r.Context()); ok && state.HasBody {
		return &state.Body, true
	}

	ctxVal := r.Context().Value(BodyContextKey)
	if ctxVal == nil {
		return nil, false
	}

	bodyCtx, ok := ctxVal.(*RequestBodyContext)
	if !ok || bodyCtx == nil {
		return nil, false
	}

	return bodyCtx, true
}

func buildUpstreamBody(rawBody []byte, isStream bool) ([]byte, bool) {
	if len(rawBody) == 0 || !isStream {
		return rawBody, false
	}

	if injected, ok := injectStreamIncludeUsageFast(rawBody); ok {
		return injected, true
	}

	inspection, err := inspectRequestBody(rawBody)
	if err != nil || !inspection.HasStreamOptions || inspection.StreamOptionsHasIncludeUsage {
		return rawBody, false
	}

	return injectIncludeUsageIntoStreamOptions(rawBody, inspection.StreamOptionsObjectStart, inspection.StreamOptionsObjectEnd), true
}

func buildUpstreamBodyFromInspection(rawBody []byte, inspection requestBodyInspection) ([]byte, bool) {
	if len(rawBody) == 0 || !inspection.Stream {
		return rawBody, false
	}

	if !inspection.HasStreamOptions {
		if injected, ok := injectStreamIncludeUsageFast(rawBody); ok {
			return injected, true
		}
		return rawBody, false
	}

	if inspection.StreamOptionsHasIncludeUsage || inspection.StreamOptionsObjectEnd <= inspection.StreamOptionsObjectStart {
		return rawBody, false
	}

	return injectIncludeUsageIntoStreamOptions(rawBody, inspection.StreamOptionsObjectStart, inspection.StreamOptionsObjectEnd), true
}

func injectStreamIncludeUsageFast(rawBody []byte) ([]byte, bool) {
	trimmed := bytes.TrimRight(rawBody, " \t\r\n")
	if len(trimmed) == 0 || trimmed[len(trimmed)-1] != '}' {
		return nil, false
	}
	if bytes.Contains(trimmed, []byte(`"stream_options"`)) {
		return nil, false
	}

	suffix := rawBody[len(trimmed):]
	injected := make([]byte, 0, len(rawBody)+len(`,"stream_options":{"include_usage":true}`))
	injected = append(injected, trimmed[:len(trimmed)-1]...)
	injected = append(injected, []byte(`,"stream_options":{"include_usage":true}`)...)
	injected = append(injected, '}')
	injected = append(injected, suffix...)
	return injected, true
}

func injectIncludeUsageIntoStreamOptions(rawBody []byte, objectStart int, objectEnd int) []byte {
	insertAt := objectEnd - 1
	if objectStart < 0 || objectStart >= objectEnd || insertAt < 0 || insertAt > len(rawBody) {
		return rawBody
	}

	separator := ","
	if len(bytes.TrimSpace(rawBody[objectStart+1:insertAt])) == 0 {
		separator = ""
	}

	const includeUsageField = `"include_usage":true`
	injected := make([]byte, 0, len(rawBody)+len(separator)+len(includeUsageField))
	injected = append(injected, rawBody[:insertAt]...)
	if separator != "" {
		injected = append(injected, separator...)
	}
	injected = append(injected, includeUsageField...)
	injected = append(injected, rawBody[insertAt:]...)
	return injected
}

func applyRequestBody(r *http.Request, body []byte) {
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
}

func extractRequestBodyMeta(body []byte) (string, bool, error) {
	inspection, err := inspectRequestBody(body)
	if err != nil {
		return "", false, err
	}
	return inspection.Model, inspection.Stream, nil
}
