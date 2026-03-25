package config

import (
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultConfigPath = "config.yaml"
	ConfigPathEnv     = "GATEWAY_CONFIG_PATH"
	MetricsPath       = "/metrics"
)

type Config struct {
	Server    ServerConfig     `yaml:"server"`
	Metrics   MetricsConfig    `yaml:"metrics"`
	Auth      AuthConfig       `yaml:"auth"`
	Upstream  UpstreamConfig   `yaml:"upstream"`
	Providers []ProviderConfig `yaml:"providers"`
}

type AuthConfig struct {
	RateLimitQPS   float64             `yaml:"rate_limit_qps"`
	RateLimitBurst int                 `yaml:"rate_limit_burst"`
	Tokens         []ClientTokenConfig `yaml:"tokens"`
}

type ClientTokenConfig struct {
	Name           string   `yaml:"name"`
	ValueEnv       string   `yaml:"value_env"`
	Value          string   `yaml:"value"`
	RateLimitQPS   float64  `yaml:"rate_limit_qps"`
	RateLimitBurst int      `yaml:"rate_limit_burst"`
	Disabled       bool     `yaml:"disabled"`
	AllowedModels  []string `yaml:"allowed_models"`
}

type ServerConfig struct {
	Host              string   `yaml:"host"`
	Port              int      `yaml:"port"`
	ReadTimeout       string   `yaml:"read_timeout"`
	ReadHeaderTimeout string   `yaml:"read_header_timeout"`
	WriteTimeout      string   `yaml:"write_timeout"`
	IdleTimeout       string   `yaml:"idle_timeout"`
	ShutdownTimeout   string   `yaml:"shutdown_timeout"`
	TrustedProxyCIDRs []string `yaml:"trusted_proxy_cidrs"`
}

type MetricsConfig struct {
	BearerTokenEnv string   `yaml:"bearer_token_env"`
	BearerToken    string   `yaml:"bearer_token"`
	AllowedCIDRs   []string `yaml:"allowed_cidrs"`
}

type UpstreamConfig struct {
	RetryableStatusCodes    []int   `yaml:"retryable_status_codes"`
	RetryBackoff            string  `yaml:"retry_backoff"`
	DefaultMaxRetries       int     `yaml:"default_max_retries"`
	HealthCheckInterval     string  `yaml:"health_check_interval"`
	HealthCheckTimeout      string  `yaml:"health_check_timeout"`
	BreakerInterval         string  `yaml:"breaker_interval"`
	BreakerTimeout          string  `yaml:"breaker_timeout"`
	BreakerFailureRatio     float64 `yaml:"breaker_failure_ratio"`
	BreakerMinimumRequests  uint32  `yaml:"breaker_minimum_requests"`
	BreakerHalfOpenRequests uint32  `yaml:"breaker_half_open_requests"`
	DefaultTimeoutNonStream string  `yaml:"default_timeout_non_stream"`
	DefaultTimeoutStream    string  `yaml:"default_timeout_stream"`
}

type ProviderConfig struct {
	Name             string   `yaml:"name"`
	BaseURL          string   `yaml:"base_url"`
	APIKeyEnv        string   `yaml:"api_key_env"`
	APIKey           string   `yaml:"api_key"`
	Models           []string `yaml:"models"`
	MaxRetries       int      `yaml:"max_retries"`
	HealthCheckPath  string   `yaml:"health_check_path"`
	TimeoutNonStream string   `yaml:"timeout_non_stream"`
	TimeoutStream    string   `yaml:"timeout_stream"`
}

var GlobalConfig *Config

func ResolveConfigPath(explicitPath string) string {
	if path := strings.TrimSpace(explicitPath); path != "" {
		return path
	}
	if path := strings.TrimSpace(os.Getenv(ConfigPathEnv)); path != "" {
		return path
	}
	return DefaultConfigPath
}

func LoadConfig(path string) error {
	resolvedPath := ResolveConfigPath(path)

	f, err := os.Open(resolvedPath)
	if err != nil {
		return fmt.Errorf("open config file: %w", err)
	}
	defer f.Close()

	cfg := &Config{}
	// Apply defaults before unmarshaling so that explicit values in YAML override them.
	setDefaults(cfg)

	decoder := yaml.NewDecoder(f)
	if err := decoder.Decode(cfg); err != nil {
		return fmt.Errorf("decode yaml config: %w", err)
	}

	if err := resolveStructuredTokens(cfg); err != nil {
		return fmt.Errorf("resolve structured tokens: %w", err)
	}
	if err := resolveProviderAPIKeys(cfg); err != nil {
		return fmt.Errorf("resolve provider api keys: %w", err)
	}
	if err := resolveMetricsConfig(cfg); err != nil {
		return fmt.Errorf("resolve metrics config: %w", err)
	}
	normalizeConfig(cfg)
	applyUpstreamDefaults(cfg)

	if err := validateConfig(cfg); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}

	GlobalConfig = cfg
	log.Printf(
		"Configuration loaded successfully. path=%s providers=%d provider_names=%s structured_tokens=%d",
		resolvedPath,
		len(cfg.Providers),
		strings.Join(providerNames(cfg.Providers), ","),
		len(cfg.Auth.Tokens),
	)
	return nil
}

func setDefaults(cfg *Config) {
	cfg.Server.Port = 8080
	cfg.Server.ReadTimeout = "300s"
	cfg.Server.ReadHeaderTimeout = "10s"
	cfg.Server.WriteTimeout = "300s"
	cfg.Server.IdleTimeout = "120s"
	cfg.Server.ShutdownTimeout = "300s"
	cfg.Auth.RateLimitQPS = 10
	cfg.Auth.RateLimitBurst = 20
	cfg.Upstream.RetryableStatusCodes = []int{429, 500, 502, 503, 504}
	cfg.Upstream.RetryBackoff = "200ms"
	cfg.Upstream.DefaultMaxRetries = 1
	cfg.Upstream.HealthCheckInterval = "15s"
	cfg.Upstream.HealthCheckTimeout = "2s"
	cfg.Upstream.BreakerInterval = "10s"
	cfg.Upstream.BreakerTimeout = "15s"
	cfg.Upstream.BreakerFailureRatio = 0.5
	cfg.Upstream.BreakerMinimumRequests = 5
	cfg.Upstream.BreakerHalfOpenRequests = 3
	cfg.Upstream.DefaultTimeoutNonStream = "120s"
	cfg.Upstream.DefaultTimeoutStream = "300s"
}

func normalizeConfig(cfg *Config) {
	cfg.Server.Host = strings.TrimSpace(cfg.Server.Host)
	cfg.Server.ReadTimeout = strings.TrimSpace(cfg.Server.ReadTimeout)
	cfg.Server.ReadHeaderTimeout = strings.TrimSpace(cfg.Server.ReadHeaderTimeout)
	cfg.Server.WriteTimeout = strings.TrimSpace(cfg.Server.WriteTimeout)
	cfg.Server.IdleTimeout = strings.TrimSpace(cfg.Server.IdleTimeout)
	cfg.Server.ShutdownTimeout = strings.TrimSpace(cfg.Server.ShutdownTimeout)
	cfg.Metrics.BearerTokenEnv = strings.TrimSpace(cfg.Metrics.BearerTokenEnv)
	cfg.Metrics.BearerToken = strings.TrimSpace(cfg.Metrics.BearerToken)
	for i := range cfg.Server.TrustedProxyCIDRs {
		cfg.Server.TrustedProxyCIDRs[i] = strings.TrimSpace(cfg.Server.TrustedProxyCIDRs[i])
	}
	cfg.Server.TrustedProxyCIDRs = uniqueNonEmpty(cfg.Server.TrustedProxyCIDRs)
	for i := range cfg.Metrics.AllowedCIDRs {
		cfg.Metrics.AllowedCIDRs[i] = strings.TrimSpace(cfg.Metrics.AllowedCIDRs[i])
	}
	cfg.Metrics.AllowedCIDRs = uniqueNonEmpty(cfg.Metrics.AllowedCIDRs)
	cfg.Upstream.RetryBackoff = strings.TrimSpace(cfg.Upstream.RetryBackoff)
	cfg.Upstream.HealthCheckInterval = strings.TrimSpace(cfg.Upstream.HealthCheckInterval)
	cfg.Upstream.HealthCheckTimeout = strings.TrimSpace(cfg.Upstream.HealthCheckTimeout)
	cfg.Upstream.BreakerInterval = strings.TrimSpace(cfg.Upstream.BreakerInterval)
	cfg.Upstream.BreakerTimeout = strings.TrimSpace(cfg.Upstream.BreakerTimeout)
	for i := range cfg.Auth.Tokens {
		token := &cfg.Auth.Tokens[i]
		token.Name = strings.TrimSpace(token.Name)
		token.ValueEnv = strings.TrimSpace(token.ValueEnv)
		token.Value = strings.TrimSpace(token.Value)
	}
	for i := range cfg.Providers {
		provider := &cfg.Providers[i]
		provider.Name = strings.TrimSpace(provider.Name)
		provider.BaseURL = strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
		provider.APIKeyEnv = strings.TrimSpace(provider.APIKeyEnv)
		provider.APIKey = strings.TrimSpace(provider.APIKey)
		provider.Models = uniqueNonEmpty(provider.Models)
		provider.HealthCheckPath = strings.TrimSpace(provider.HealthCheckPath)
	}
}

func resolveStructuredTokens(cfg *Config) error {
	for i := range cfg.Auth.Tokens {
		token := &cfg.Auth.Tokens[i]
		if token.Disabled {
			continue
		}
		if strings.TrimSpace(token.Value) != "" {
			return fmt.Errorf("auth.tokens[%d] (%s) must not set inline value; use value_env instead", i, token.Name)
		}
		if token.ValueEnv == "" {
			return fmt.Errorf("auth.tokens[%d] (%s) must set value_env", i, token.Name)
		}
		envValue, ok := os.LookupEnv(token.ValueEnv)
		if !ok || strings.TrimSpace(envValue) == "" {
			return fmt.Errorf("auth.tokens[%d] (%s) requires environment variable %s", i, token.Name, token.ValueEnv)
		}
		token.Value = strings.TrimSpace(envValue)
	}
	return nil
}

func resolveProviderAPIKeys(cfg *Config) error {
	for i := range cfg.Providers {
		provider := &cfg.Providers[i]
		if strings.TrimSpace(provider.APIKey) != "" {
			return fmt.Errorf("provider %q still uses inline api_key in config file; remove it and use api_key_env instead", provider.Name)
		}
		envName := strings.TrimSpace(provider.APIKeyEnv)
		if envName == "" {
			continue
		}
		envValue, ok := os.LookupEnv(envName)
		if !ok || strings.TrimSpace(envValue) == "" {
			return fmt.Errorf("provider %q requires environment variable %s, but it is not set", provider.Name, envName)
		}
		provider.APIKey = strings.TrimSpace(envValue)
	}
	return nil
}

func resolveMetricsConfig(cfg *Config) error {
	if strings.TrimSpace(cfg.Metrics.BearerToken) != "" {
		return fmt.Errorf("metrics.bearer_token must not be set in config file; use metrics.bearer_token_env instead")
	}
	envName := strings.TrimSpace(cfg.Metrics.BearerTokenEnv)
	if envName != "" {
		envValue, ok := os.LookupEnv(envName)
		if ok && strings.TrimSpace(envValue) != "" {
			cfg.Metrics.BearerToken = strings.TrimSpace(envValue)
		}
	}
	return nil
}

func applyUpstreamDefaults(cfg *Config) {
	if cfg.Upstream.DefaultMaxRetries < 0 {
		cfg.Upstream.DefaultMaxRetries = 0
	}
	if len(cfg.Upstream.RetryableStatusCodes) == 0 {
		cfg.Upstream.RetryableStatusCodes = []int{httpStatusTooManyRequests, 500, 502, 503, 504}
	}
	if strings.TrimSpace(cfg.Upstream.RetryBackoff) == "" {
		cfg.Upstream.RetryBackoff = "200ms"
	}
	if strings.TrimSpace(cfg.Upstream.HealthCheckInterval) == "" {
		cfg.Upstream.HealthCheckInterval = "15s"
	}
	if strings.TrimSpace(cfg.Upstream.HealthCheckTimeout) == "" {
		cfg.Upstream.HealthCheckTimeout = "2s"
	}
	if strings.TrimSpace(cfg.Upstream.BreakerInterval) == "" {
		cfg.Upstream.BreakerInterval = "10s"
	}
	if strings.TrimSpace(cfg.Upstream.BreakerTimeout) == "" {
		cfg.Upstream.BreakerTimeout = "15s"
	}
	if cfg.Upstream.BreakerFailureRatio <= 0 || cfg.Upstream.BreakerFailureRatio > 1 {
		cfg.Upstream.BreakerFailureRatio = 0.5
	}
	if cfg.Upstream.BreakerMinimumRequests == 0 {
		cfg.Upstream.BreakerMinimumRequests = 5
	}
	if cfg.Upstream.BreakerHalfOpenRequests == 0 {
		cfg.Upstream.BreakerHalfOpenRequests = 3
	}
	for i := range cfg.Providers {
		if cfg.Providers[i].MaxRetries < 0 {
			cfg.Providers[i].MaxRetries = 0
		}
	}
}

func validateConfig(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535")
	}
	for _, durationValue := range []struct{ name, value string }{
		{"server.read_timeout", cfg.Server.ReadTimeout},
		{"server.read_header_timeout", cfg.Server.ReadHeaderTimeout},
		{"server.write_timeout", cfg.Server.WriteTimeout},
		{"server.idle_timeout", cfg.Server.IdleTimeout},
		{"server.shutdown_timeout", cfg.Server.ShutdownTimeout},
		{"upstream.retry_backoff", cfg.Upstream.RetryBackoff},
		{"upstream.health_check_interval", cfg.Upstream.HealthCheckInterval},
		{"upstream.health_check_timeout", cfg.Upstream.HealthCheckTimeout},
		{"upstream.breaker_interval", cfg.Upstream.BreakerInterval},
		{"upstream.breaker_timeout", cfg.Upstream.BreakerTimeout},
		{"upstream.default_timeout_non_stream", cfg.Upstream.DefaultTimeoutNonStream},
		{"upstream.default_timeout_stream", cfg.Upstream.DefaultTimeoutStream},
	} {
		if err := validatePositiveDuration(durationValue.name, durationValue.value); err != nil {
			return err
		}
	}
	if cfg.Auth.RateLimitQPS <= 0 {
		return fmt.Errorf("auth.rate_limit_qps must be > 0")
	}
	if cfg.Auth.RateLimitBurst <= 0 {
		return fmt.Errorf("auth.rate_limit_burst must be > 0")
	}
	if len(cfg.Auth.Tokens) == 0 {
		return fmt.Errorf("at least one gateway token must be configured via auth.tokens")
	}
	for _, cidr := range cfg.Server.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid trusted proxy cidr %q: %w", cidr, err)
		}
	}
	for _, cidr := range cfg.Metrics.AllowedCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid metrics allowed cidr %q: %w", cidr, err)
		}
	}
	if len(cfg.Providers) == 0 {
		return fmt.Errorf("at least one provider must be configured")
	}
	seenProviders := make(map[string]struct{}, len(cfg.Providers))
	for _, provider := range cfg.Providers {
		if provider.Name == "" {
			return fmt.Errorf("provider name cannot be empty")
		}
		if _, exists := seenProviders[provider.Name]; exists {
			return fmt.Errorf("duplicate provider name: %s", provider.Name)
		}
		seenProviders[provider.Name] = struct{}{}
		if err := validateProviderBaseURL(provider.Name, provider.BaseURL); err != nil {
			return err
		}
		if len(provider.Models) == 0 {
			return fmt.Errorf("provider %q must configure at least one model", provider.Name)
		}
		if provider.MaxRetries < 0 {
			return fmt.Errorf("provider %q max_retries must be greater than or equal to 0", provider.Name)
		}
		if t := strings.TrimSpace(provider.TimeoutNonStream); t != "" {
			if err := validatePositiveDuration(fmt.Sprintf("provider[%s].timeout_non_stream", provider.Name), t); err != nil {
				return err
			}
		}
		if t := strings.TrimSpace(provider.TimeoutStream); t != "" {
			if err := validatePositiveDuration(fmt.Sprintf("provider[%s].timeout_stream", provider.Name), t); err != nil {
				return err
			}
		}
	}
	return validateTokenCatalog(cfg.Auth)
}

func validateTokenCatalog(auth AuthConfig) error {
	seenNames := make(map[string]struct{}, len(auth.Tokens))
	seenValues := make(map[string]struct{}, len(auth.Tokens))
	for i, token := range auth.Tokens {
		if token.Disabled {
			continue
		}
		if token.Name == "" {
			return fmt.Errorf("auth.tokens[%d].name cannot be empty", i)
		}
		if _, ok := seenNames[token.Name]; ok {
			return fmt.Errorf("duplicate auth token name: %s", token.Name)
		}
		seenNames[token.Name] = struct{}{}
		if token.Value == "" {
			return fmt.Errorf("auth.tokens[%d] (%s) resolved empty token value", i, token.Name)
		}
		if _, ok := seenValues[token.Value]; ok {
			return fmt.Errorf("duplicate gateway token material detected for auth.tokens[%d] (%s)", i, token.Name)
		}
		seenValues[token.Value] = struct{}{}
		if token.RateLimitQPS < 0 {
			return fmt.Errorf("auth.tokens[%d] (%s) rate_limit_qps must be >= 0", i, token.Name)
		}
		if token.RateLimitBurst < 0 {
			return fmt.Errorf("auth.tokens[%d] (%s) rate_limit_burst must be >= 0", i, token.Name)
		}
	}
	return nil
}

const httpStatusTooManyRequests = 429

func validatePositiveDuration(fieldName, raw string) error {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%s is invalid: %w", fieldName, err)
	}
	if d <= 0 {
		return fmt.Errorf("%s must be > 0", fieldName)
	}
	return nil
}

func validateProviderBaseURL(providerName, raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("provider %q base_url is invalid: %w", providerName, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("provider %q base_url must include scheme and host", providerName)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if isPrivateOrLoopbackHost(parsed.Hostname()) {
			return nil
		}
		return fmt.Errorf("provider %q base_url must use https unless it points to a private or loopback host", providerName)
	default:
		return fmt.Errorf("provider %q base_url must use http or https", providerName)
	}
}

func isPrivateOrLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}

func providerNames(providers []ProviderConfig) []string {
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		if provider.Name != "" {
			names = append(names, provider.Name)
		}
	}
	sort.Strings(names)
	return names
}

func uniqueNonEmpty(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		normalized := strings.TrimSpace(item)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}
