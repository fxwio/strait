package adapter

import (
	"net/http"
	"net/url"
)

// Provider defines the interface for different LLM backends.
type Provider interface {
	Name() string
	CompileRequest(targetURL *url.URL, origPath string, origHeader http.Header, body []byte, apiKey string) (path string, newBody []byte, newHeader http.Header, err error)
	TranslateResponse(resp *http.Response, w http.ResponseWriter, model string) error
	// GenerateProbeRequest creates a minimal request to verify the provider is functional.
	GenerateProbeRequest(targetURL *url.URL, apiKey string, model string) (*http.Request, error)
}

var providers = map[string]Provider{
	"openai":      &OpenAIProvider{},
	"anthropic":   &AnthropicProvider{},
	"siliconflow": &SiliconFlowProvider{},
}

func GetProvider(name string) Provider {
	if p, ok := providers[name]; ok {
		return p
	}
	return providers["openai"]
}
