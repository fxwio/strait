package middleware

import (
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/fxwio/strait/internal/config"
	"github.com/fxwio/strait/internal/model"
	"github.com/fxwio/strait/internal/response"
)

// 定义 context key 类型，避免与其他包的 Context Key 冲突。
type contextKey string

const GatewayContextKey contextKey = "gateway_ctx"

const (
	routePolicyConfiguredOrder = "configured_order"
	routePolicyHealthyFirst    = "healthy_then_configured_order"
)

type routeEntry struct {
	candidates      []model.ProviderRoute
	routeCandidates model.RouteCandidateTraceList
}

type routeTable struct {
	cfg     *config.Config
	byModel map[string]routeEntry
}

var (
	routeTableCache  atomic.Pointer[routeTable]
	routeTableMu     sync.Mutex
	healthResolver   providerHealthResolver
	healthResolverMu sync.RWMutex
)

type providerHealthResolver func(candidate model.ProviderRoute) (known bool, healthy bool)

// ModelRouterMiddleware 负责解析请求中的 model，并注入 GatewayContext。
// 一个模型可以命中多个 provider，最终由 proxy 层做故障切换。
func ModelRouterMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyCtx, ok := GetRequestBodyContext(r)
		if !ok {
			response.WriteOpenAIError(
				w,
				http.StatusInternalServerError,
				"Request body context missing.",
				"server_error",
				nil,
				response.Ptr("missing_body_context"),
			)
			return
		}

		if bodyCtx.DecodeError != nil {
			response.WriteOpenAIError(
				w,
				http.StatusBadRequest,
				"Invalid JSON payload.",
				"invalid_request_error",
				nil,
				response.Ptr("invalid_json"),
			)
			return
		}

		if bodyCtx.RequestedModel == "" {
			response.WriteOpenAIError(
				w,
				http.StatusBadRequest,
				"Missing required field: model.",
				"invalid_request_error",
				response.Ptr("model"),
				response.Ptr("missing_required_field"),
			)
			return
		}

		route, routePolicy, ok := lookupRoute(bodyCtx.RequestedModel)
		if !ok {
			response.WriteOpenAIError(
				w,
				http.StatusNotFound,
				"The model '"+bodyCtx.RequestedModel+"' does not exist or is not available on this gateway.",
				"invalid_request_error",
				response.Ptr("model"),
				response.Ptr("model_not_found"),
			)
			return
		}

		gatewayCtx := model.GatewayContext{
			RequestedModel:       bodyCtx.RequestedModel,
			TargetModel:          bodyCtx.RequestedModel,
			CandidateProviders:   route.candidates,
			RouteSelectionPolicy: routePolicy,
			RouteCandidates:      route.routeCandidates,
		}
		gatewayCtx.SetActiveProvider(route.candidates[0])

		r = putGatewayContext(r, gatewayCtx)
		next.ServeHTTP(w, r)
	})
}

func matchProviders(targetModel string) []model.ProviderRoute {
	route, _, ok := lookupRoute(targetModel)
	if !ok {
		return nil
	}
	return route.candidates
}

func WarmRouteTable() {
	_ = currentRouteTable()
}

func lookupRoute(targetModel string) (routeEntry, string, bool) {
	if targetModel == "" {
		return routeEntry{}, "", false
	}
	table := currentRouteTable()
	if table == nil {
		return routeEntry{}, "", false
	}
	entry, ok := table.byModel[targetModel]
	if !ok {
		return routeEntry{}, "", false
	}
	resolved, policy := applyHealthAwareRouting(entry)
	return resolved, policy, true
}

func currentRouteTable() *routeTable {
	cfg := config.GlobalConfig
	if cfg == nil {
		return nil
	}

	if cached := routeTableCache.Load(); cached != nil && cached.cfg == cfg {
		return cached
	}

	routeTableMu.Lock()
	defer routeTableMu.Unlock()

	if cached := routeTableCache.Load(); cached != nil && cached.cfg == cfg {
		return cached
	}

	built := buildRouteTable(cfg)
	routeTableCache.Store(built)
	return built
}

func buildRouteTable(cfg *config.Config) *routeTable {
	table := &routeTable{
		cfg:     cfg,
		byModel: make(map[string]routeEntry),
	}

	if cfg == nil {
		return table
	}

	byModelProviders := make(map[string][]model.ProviderRoute)
	for i := range cfg.Providers {
		provider := cfg.Providers[i]
		route := model.ProviderRoute{
			Name:             provider.Name,
			BaseURL:          provider.BaseURL,
			APIKey:           provider.APIKey,
			Priority:         i + 1,
			MaxRetries:       provider.MaxRetries,
			HealthCheckPath:  provider.HealthCheckPath,
			TimeoutNonStream: provider.TimeoutNonStream,
			TimeoutStream:    provider.TimeoutStream,
		}
		for _, supportedModel := range provider.Models {
			byModelProviders[supportedModel] = append(byModelProviders[supportedModel], route)
		}
	}

	for modelName, providers := range byModelProviders {
		table.byModel[modelName] = routeEntry{
			candidates:      providers,
			routeCandidates: buildRouteCandidates(providers),
		}
	}

	return table
}

// SetProviderHealthResolver injects a resolver used by routing to prefer
// currently healthy providers over known-unhealthy ones. Proxy health
// monitoring wires this once during gateway startup; tests may replace it.
func SetProviderHealthResolver(resolver func(candidate model.ProviderRoute) (known bool, healthy bool)) {
	healthResolverMu.Lock()
	defer healthResolverMu.Unlock()
	healthResolver = resolver
}

func getProviderHealthResolver() providerHealthResolver {
	healthResolverMu.RLock()
	defer healthResolverMu.RUnlock()
	return healthResolver
}

func applyHealthAwareRouting(entry routeEntry) (routeEntry, string) {
	resolver := getProviderHealthResolver()
	if resolver == nil || len(entry.candidates) < 2 {
		return entry, routePolicyConfiguredOrder
	}

	healthyCandidates := make([]model.ProviderRoute, 0, len(entry.candidates))
	unhealthyCandidates := make([]model.ProviderRoute, 0, len(entry.candidates))
	healthyTraces := make(model.RouteCandidateTraceList, 0, len(entry.routeCandidates))
	unhealthyTraces := make(model.RouteCandidateTraceList, 0, len(entry.routeCandidates))

	sawUnhealthy := false
	reordered := false

	for i, candidate := range entry.candidates {
		trace := entry.routeCandidates[i]
		known, healthy := resolver(candidate)
		if known && !healthy {
			sawUnhealthy = true
			unhealthyCandidates = append(unhealthyCandidates, candidate)
			unhealthyTraces = append(unhealthyTraces, trace)
			continue
		}
		if sawUnhealthy {
			reordered = true
		}
		healthyCandidates = append(healthyCandidates, candidate)
		healthyTraces = append(healthyTraces, trace)
	}

	if !reordered {
		return entry, routePolicyConfiguredOrder
	}

	candidates := make([]model.ProviderRoute, 0, len(entry.candidates))
	candidates = append(candidates, healthyCandidates...)
	candidates = append(candidates, unhealthyCandidates...)

	routeCandidates := make(model.RouteCandidateTraceList, 0, len(entry.routeCandidates))
	routeCandidates = append(routeCandidates, healthyTraces...)
	routeCandidates = append(routeCandidates, unhealthyTraces...)

	return routeEntry{
		candidates:      candidates,
		routeCandidates: routeCandidates,
	}, routePolicyHealthyFirst
}

func buildRouteCandidates(candidates []model.ProviderRoute) model.RouteCandidateTraceList {
	traces := make(model.RouteCandidateTraceList, 0, len(candidates))
	for _, candidate := range candidates {
		traces = append(traces, model.RouteCandidateTrace{
			Provider: candidate.Name,
			Priority: candidate.Priority,
		})
	}
	return traces
}
