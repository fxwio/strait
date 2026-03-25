package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/fxwio/strait/internal/config"
	"github.com/fxwio/strait/internal/response"
)

type clientAuthContextKey string

const ClientAuthContextKey clientAuthContextKey = "client_auth_ctx"

type ClientAuthContext struct {
	Token          string
	Fingerprint    string
	TokenName      string
	RateLimitQPS   float64
	RateLimitBurst int
	AllowedModels  []string
}

// TokenIdentity represents a validated token with its configuration.
type TokenIdentity struct {
	Token          string
	Fingerprint    string
	Name           string
	RateLimitQPS   float64
	RateLimitBurst int
	AllowedModels  []string
}

var (
	tokenRegistry   map[string]TokenIdentity
	tokenRegistryMu sync.RWMutex
	registryInited  bool
	registryInitMu  sync.Mutex
)

func ensureTokenRegistry() {
	tokenRegistryMu.RLock()
	inited := registryInited
	tokenRegistryMu.RUnlock()
	if inited {
		return
	}
	registryInitMu.Lock()
	defer registryInitMu.Unlock()
	if !registryInited {
		rebuildTokenRegistry()
		registryInited = true
	}
}

func rebuildTokenRegistry() {
	tokenRegistryMu.Lock()
	defer tokenRegistryMu.Unlock()

	next := make(map[string]TokenIdentity)
	cfg := config.GlobalConfig
	if cfg == nil {
		tokenRegistry = next
		return
	}

	for _, item := range cfg.Auth.Tokens {
		if item.Disabled {
			continue
		}
		token := strings.TrimSpace(item.Value)
		if token == "" {
			continue
		}
		fp := tokenFingerprint(token)
		identity := TokenIdentity{
			Token:          token,
			Fingerprint:    fp,
			Name:           item.Name,
			RateLimitQPS:   item.RateLimitQPS,
			RateLimitBurst: item.RateLimitBurst,
			AllowedModels:  normalizeStringSlice(item.AllowedModels),
		}
		next[token] = identity
	}

	tokenRegistry = next
}

func resolveToken(raw string) (TokenIdentity, bool) {
	ensureTokenRegistry()
	tokenRegistryMu.RLock()
	defer tokenRegistryMu.RUnlock()
	identity, ok := tokenRegistry[strings.TrimSpace(raw)]
	return identity, ok
}

func tokenFingerprint(raw string) string {
	token := strings.TrimSpace(raw)
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:8]) + ":len=" + strconv.Itoa(len(token))
}

func normalizeStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// AuthMiddleware validates the client Bearer Token.
// On success, it stores token identity in the request context for rate limiting and auditing.
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if authHeader == "" {
			response.WriteAuthenticationError(w, http.StatusUnauthorized, "Missing Authorization header.", "missing_authorization_header")
			return
		}
		if !strings.HasPrefix(authHeader, "Bearer ") {
			response.WriteAuthenticationError(w, http.StatusUnauthorized, "Invalid Authorization header. Expected 'Bearer '.", "invalid_authorization_header")
			return
		}

		clientToken := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		if clientToken == "" {
			response.WriteAuthenticationError(w, http.StatusUnauthorized, "Missing bearer token.", "missing_bearer_token")
			return
		}

		identity, ok := resolveToken(clientToken)
		if !ok {
			response.WriteAuthenticationError(w, http.StatusUnauthorized, "Invalid API key provided.", "invalid_api_key")
			return
		}

		authCtx := ClientAuthContext{
			Token:          clientToken,
			Fingerprint:    identity.Fingerprint,
			TokenName:      identity.Name,
			RateLimitQPS:   identity.RateLimitQPS,
			RateLimitBurst: identity.RateLimitBurst,
			AllowedModels:  identity.AllowedModels,
		}
		r = putClientAuthContext(r, authCtx)
		next.ServeHTTP(w, r)
	})
}

func GetClientAuthContext(r *http.Request) (*ClientAuthContext, bool) {
	if r == nil {
		return nil, false
	}
	if state, ok := getRequestState(r.Context()); ok && state.HasAuth {
		return &state.Auth, true
	}

	ctxVal := r.Context().Value(ClientAuthContextKey)
	if ctxVal == nil {
		return nil, false
	}
	authCtx, ok := ctxVal.(*ClientAuthContext)
	if !ok || authCtx == nil {
		return nil, false
	}
	return authCtx, true
}
