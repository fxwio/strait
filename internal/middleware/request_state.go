package middleware

import (
	"context"
	"net/http"

	"github.com/fxwio/strait/internal/model"
)

const requestStateContextKey contextKey = "request_state_ctx"

type RequestState struct {
	Meta       RequestMetaContext
	HasMeta    bool
	Auth       ClientAuthContext
	HasAuth    bool
	Body       RequestBodyContext
	HasBody    bool
	Gateway    model.GatewayContext
	HasGateway bool
}

func ensureRequestState(r *http.Request) (*RequestState, *http.Request) {
	if r == nil {
		return nil, nil
	}
	if state, ok := getRequestState(r.Context()); ok {
		return state, r
	}

	state := &RequestState{}
	ctx := context.WithValue(r.Context(), requestStateContextKey, state)
	return state, r.WithContext(ctx)
}

func getRequestState(ctx context.Context) (*RequestState, bool) {
	if ctx == nil {
		return nil, false
	}

	state, ok := ctx.Value(requestStateContextKey).(*RequestState)
	if !ok || state == nil {
		return nil, false
	}
	return state, true
}

func putRequestMeta(r *http.Request, meta RequestMetaContext) *http.Request {
	state, nextReq := ensureRequestState(r)
	state.Meta = meta
	state.HasMeta = true
	return nextReq
}

func putClientAuthContext(r *http.Request, auth ClientAuthContext) *http.Request {
	state, nextReq := ensureRequestState(r)
	state.Auth = auth
	state.HasAuth = true
	return nextReq
}

func putRequestBodyContext(r *http.Request, body RequestBodyContext) *http.Request {
	state, nextReq := ensureRequestState(r)
	state.Body = body
	state.HasBody = true
	return nextReq
}

func putGatewayContext(r *http.Request, gateway model.GatewayContext) *http.Request {
	state, nextReq := ensureRequestState(r)
	state.Gateway = gateway
	state.HasGateway = true
	return nextReq
}

func GetGatewayContext(r *http.Request) (*model.GatewayContext, bool) {
	if r == nil {
		return nil, false
	}
	if state, ok := getRequestState(r.Context()); ok && state.HasGateway {
		return &state.Gateway, true
	}

	ctxVal := r.Context().Value(GatewayContextKey)
	if ctxVal == nil {
		return nil, false
	}

	gatewayCtx, ok := ctxVal.(*model.GatewayContext)
	if !ok || gatewayCtx == nil {
		return nil, false
	}
	return gatewayCtx, true
}
