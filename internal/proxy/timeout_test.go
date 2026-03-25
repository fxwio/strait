package proxy

import (
	"testing"
	"time"

	"github.com/fxwio/strait/internal/config"
	"github.com/fxwio/strait/internal/model"
	"github.com/fxwio/strait/pkg/logger"
)

func init() {
	logger.InitLogger()
}

// baseConfig returns a minimal GlobalConfig for timeout tests.
func baseConfig(globalNonStream, globalStream string) func() {
	orig := config.GlobalConfig
	cfg := &config.Config{}
	if orig != nil {
		*cfg = *orig
	}
	cfg.Upstream.DefaultTimeoutNonStream = globalNonStream
	cfg.Upstream.DefaultTimeoutStream = globalStream
	config.GlobalConfig = cfg
	return func() { config.GlobalConfig = orig }
}

func TestProviderTimeout_UsesProviderNonStream(t *testing.T) {
	cleanup := baseConfig("120s", "300s")
	defer cleanup()

	p := model.ProviderRoute{TimeoutNonStream: "45s"}
	got := providerTimeout(p, false)
	if got != 45*time.Second {
		t.Errorf("got %v, want 45s", got)
	}
}

func TestProviderTimeout_UsesProviderStream(t *testing.T) {
	cleanup := baseConfig("120s", "300s")
	defer cleanup()

	p := model.ProviderRoute{TimeoutStream: "600s"}
	got := providerTimeout(p, true)
	if got != 600*time.Second {
		t.Errorf("got %v, want 600s", got)
	}
}

func TestProviderTimeout_FallsBackToGlobalNonStream(t *testing.T) {
	cleanup := baseConfig("90s", "400s")
	defer cleanup()

	p := model.ProviderRoute{} // no per-provider overrides
	got := providerTimeout(p, false)
	if got != 90*time.Second {
		t.Errorf("got %v, want 90s (global default)", got)
	}
}

func TestProviderTimeout_FallsBackToGlobalStream(t *testing.T) {
	cleanup := baseConfig("90s", "400s")
	defer cleanup()

	p := model.ProviderRoute{}
	got := providerTimeout(p, true)
	if got != 400*time.Second {
		t.Errorf("got %v, want 400s (global default)", got)
	}
}

func TestProviderTimeout_HardCodedFallbackNonStream(t *testing.T) {
	cleanup := baseConfig("", "") // no global override
	defer cleanup()

	p := model.ProviderRoute{}
	got := providerTimeout(p, false)
	if got != defaultNonStreamTimeout {
		t.Errorf("got %v, want %v (hard-coded fallback)", got, defaultNonStreamTimeout)
	}
}

func TestProviderTimeout_HardCodedFallbackStream(t *testing.T) {
	cleanup := baseConfig("", "")
	defer cleanup()

	p := model.ProviderRoute{}
	got := providerTimeout(p, true)
	if got != defaultStreamTimeout {
		t.Errorf("got %v, want %v (hard-coded fallback)", got, defaultStreamTimeout)
	}
}

func TestProviderTimeout_ProviderOverridesGlobal(t *testing.T) {
	// Provider sets 30 s; global says 120 s — provider wins.
	cleanup := baseConfig("120s", "300s")
	defer cleanup()

	p := model.ProviderRoute{TimeoutNonStream: "30s", TimeoutStream: "180s"}

	if got := providerTimeout(p, false); got != 30*time.Second {
		t.Errorf("non-stream: got %v, want 30s", got)
	}
	if got := providerTimeout(p, true); got != 180*time.Second {
		t.Errorf("stream: got %v, want 180s", got)
	}
}

func TestProviderTimeout_InvalidDurationIgnored(t *testing.T) {
	cleanup := baseConfig("120s", "300s")
	defer cleanup()

	p := model.ProviderRoute{TimeoutNonStream: "not-a-duration"}
	got := providerTimeout(p, false)
	// Falls through to global default.
	if got != 120*time.Second {
		t.Errorf("got %v, want 120s (global fallback on invalid duration)", got)
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"5s", 5 * time.Second},
		{"1m30s", 90 * time.Second},
		{"", 0},
		{"bad", 0},
		{"-1s", 0},
	}
	for _, tc := range cases {
		got := parseDuration(tc.in)
		if got != tc.want {
			t.Errorf("parseDuration(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
