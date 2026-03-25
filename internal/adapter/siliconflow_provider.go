package adapter

// SiliconFlow speaks the OpenAI-compatible chat completions protocol, so the
// gateway only needs a thin name alias over the existing OpenAI adapter.
type SiliconFlowProvider struct {
	OpenAIProvider
}

func (p *SiliconFlowProvider) Name() string {
	return "siliconflow"
}
