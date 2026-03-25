package proxy

import (
	"time"

	"github.com/fxwio/strait/internal/config"
	"github.com/fxwio/strait/internal/model"
)

const (
	defaultNonStreamTimeout = 120 * time.Second
	defaultStreamTimeout    = 300 * time.Second
)

// providerTimeout returns the per-attempt deadline for an upstream request.
// Resolution order (first non-zero value wins):
//  1. provider-level timeout_non_stream / timeout_stream
//  2. upstream.default_timeout_non_stream / upstream.default_timeout_stream
//  3. hard-coded fallback (120 s non-stream, 300 s stream)
func providerTimeout(provider model.ProviderRoute, isStream bool) time.Duration {
	upstreamCfg := config.GlobalConfig.Upstream

	if isStream {
		if d := parseDuration(provider.TimeoutStream); d > 0 {
			return d
		}
		if d := parseDuration(upstreamCfg.DefaultTimeoutStream); d > 0 {
			return d
		}
		return defaultStreamTimeout
	}

	if d := parseDuration(provider.TimeoutNonStream); d > 0 {
		return d
	}
	if d := parseDuration(upstreamCfg.DefaultTimeoutNonStream); d > 0 {
		return d
	}
	return defaultNonStreamTimeout
}

func parseDuration(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}
