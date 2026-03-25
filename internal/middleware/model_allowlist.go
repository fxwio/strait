package middleware

import (
	"net/http"
	"strings"

	"github.com/fxwio/strait/internal/response"
	"github.com/fxwio/strait/pkg/logger"
)

// ModelAllowlistMiddleware enforces the per-token model whitelist.
//
// If the authenticated token has a non-empty AllowedModels list, the requested
// model must appear in that list.
// A mismatch returns HTTP 403 with an OpenAI-compatible error body.
//
// Tokens with an empty AllowedModels list have no restriction and pass through
// unconditionally. Unauthenticated requests (no ClientAuthContext) also pass
// through so the auth middleware can handle them first.
//
// Must run after: BodyContextMiddleware, AuthMiddleware.
// Must run before: ModelRouterMiddleware.
func ModelAllowlistMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authCtx, ok := GetClientAuthContext(r)
		if !ok || len(authCtx.AllowedModels) == 0 {
			// No auth context or no restriction — pass through.
			next.ServeHTTP(w, r)
			return
		}

		bodyCtx, ok := GetRequestBodyContext(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}

		if bodyCtx.DecodeError != nil {
			next.ServeHTTP(w, r)
			return
		}

		requestedModel := bodyCtx.RequestedModel
		if requestedModel == "" {
			// No model field — let the model router handle the error.
			next.ServeHTTP(w, r)
			return
		}

		if !isModelAllowed(requestedModel, authCtx.AllowedModels) {
			logger.Log.Warn("Model allowlist violation",
				"token_name", authCtx.TokenName,
				"requested_model", requestedModel,
				"allowed_models", authCtx.AllowedModels,
			)
			response.WritePermissionError(
				w,
				http.StatusForbidden,
				"The model '"+requestedModel+"' is not allowed for this API key.",
				"model",
				"model_not_allowed",
			)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// isModelAllowed reports whether model appears in the allowlist.
// Comparison is case-insensitive so "GPT-4O" matches "gpt-4o".
func isModelAllowed(model string, allowlist []string) bool {
	lower := strings.ToLower(model)
	for _, allowed := range allowlist {
		if strings.ToLower(allowed) == lower {
			return true
		}
	}
	return false
}
