package adapter

import "testing"

func TestGetProvider_SiliconFlow(t *testing.T) {
	provider := GetProvider("siliconflow")
	if provider == nil {
		t.Fatal("provider is nil")
	}
	if got := provider.Name(); got != "siliconflow" {
		t.Fatalf("provider name = %q, want %q", got, "siliconflow")
	}
}
