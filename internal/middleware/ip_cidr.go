package middleware

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// parseCIDRs 负责把配置里的 CIDR 字符串解析为 []*net.IPNet。
// 配置层虽然已经做过一次校验，但运行时这里仍然保留兜底，避免被不一致状态拖垮。
func parseCIDRs(cidrs []string, fieldName string) ([]*net.IPNet, error) {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}

		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("invalid %s cidr %q: %w", fieldName, cidr, err)
		}
		nets = append(nets, ipNet)
	}

	return nets, nil
}

// remoteIP 只从 RemoteAddr 取直接来源 IP，不读取任何转发头。
// 这是 /metrics 授权判断应当使用的语义。
func remoteIP(r *http.Request) string {
	if r == nil {
		return ""
	}

	remoteAddr := strings.TrimSpace(r.RemoteAddr)
	if remoteAddr == "" {
		return ""
	}

	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return strings.TrimSpace(host)
	}

	return remoteAddr
}

func ipInCIDRs(ipStr string, nets []*net.IPNet) bool {
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return false
	}

	for _, ipNet := range nets {
		if ipNet != nil && ipNet.Contains(ip) {
			return true
		}
	}

	return false
}

// extractClientIPFromTrustedProxy 只有在直连来源 IP 属于 trusted proxy 网段时，
// 才会信任 X-Forwarded-For / X-Real-IP。
func extractClientIPFromTrustedProxy(r *http.Request, trustedProxyCIDRs []*net.IPNet) string {
	remote := remoteIP(r)
	if !ipInCIDRs(remote, trustedProxyCIDRs) {
		return remote
	}

	if ip := firstValidForwardedIP(r.Header.Get("X-Forwarded-For")); ip != "" {
		return ip
	}

	if ip := normalizeIPCandidate(r.Header.Get("X-Real-IP")); ip != "" {
		return ip
	}

	return remote
}

// X-Forwarded-For 通常是 client, proxy1, proxy2...
// 这里取第一个合法 IP，既兼容标准链路，也能避免把 "unknown" 之类脏值当成 client IP。
func firstValidForwardedIP(xff string) string {
	if strings.TrimSpace(xff) == "" {
		return ""
	}

	parts := strings.Split(xff, ",")
	for _, part := range parts {
		if ip := normalizeIPCandidate(part); ip != "" {
			return ip
		}
	}

	return ""
}

func normalizeIPCandidate(candidate string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return ""
	}

	// 先尝试直接解析纯 IP
	if ip := net.ParseIP(trimIPv6Brackets(candidate)); ip != nil {
		return ip.String()
	}

	// 再尝试解析 host:port
	host, _, err := net.SplitHostPort(candidate)
	if err != nil {
		return ""
	}

	host = strings.TrimSpace(trimIPv6Brackets(host))
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}

	return ""
}

func trimIPv6Brackets(s string) string {
	if len(s) >= 2 && strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		return s[1 : len(s)-1]
	}
	return s
}
