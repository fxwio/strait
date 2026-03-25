package adapter

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type OpenAIProvider struct{}

func (p *OpenAIProvider) Name() string {
	return "openai"
}

func (p *OpenAIProvider) CompileRequest(targetURL *url.URL, origPath string, origHeader http.Header, body []byte, apiKey string) (string, []byte, http.Header, error) {
	header := origHeader
	if apiKey != "" {
		header.Set("Authorization", "Bearer "+apiKey)
	}

	return joinURLPath(targetURL.Path, origPath), body, header, nil
}

func joinURLPath(basePath, reqPath string) string {
	switch {
	case basePath == "":
		if reqPath == "" {
			return "/"
		}
		return reqPath
	case reqPath == "":
		return basePath
	default:
		return strings.TrimRight(basePath, "/") + "/" + strings.TrimLeft(reqPath, "/")
	}
}

func (p *OpenAIProvider) GenerateProbeRequest(targetURL *url.URL, apiKey string, model string) (*http.Request, error) {
	path := joinURLPath(targetURL.Path, "/v1/chat/completions")
	u := *targetURL
	u.Path = path

	payload := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}],"max_tokens":1}`, model)
	req, err := http.NewRequest(http.MethodPost, u.String(), strings.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	return req, nil
}

func (p *OpenAIProvider) TranslateResponse(resp *http.Response, w http.ResponseWriter, model string) error {
	for key, values := range resp.Header {
		if shouldSkipResponseHeader(key) {
			continue
		}
		w.Header()[key] = values
	}
	w.Header().Del("Server")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(resp.StatusCode)

	_, err := io.Copy(w, resp.Body)
	return err
}

func shouldSkipResponseHeader(key string) bool {
	switch http.CanonicalHeaderKey(key) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}
