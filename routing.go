package dbsites

import (
	"net"
	"net/http"
	"path"
	"strings"
)

type routeKind string

const (
	routeCustomDomain routeKind = "custom_domain"
)

type routeTarget struct {
	Kind        routeKind
	Host        string
	RequestPath string
	PageSlug    string
}

func normalizeHost(hostport string) string {
	h, _, err := net.SplitHostPort(hostport)
	if err != nil {
		h = hostport
	}
	return strings.ToLower(strings.TrimSpace(strings.TrimSuffix(h, ".")))
}

func effectiveRequestHost(r *http.Request) string {
	if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" {
		first := strings.TrimSpace(strings.Split(xfh, ",")[0])
		if host := normalizeHost(first); host != "" {
			return host
		}
	}
	return normalizeHost(r.Host)
}

func resolveTarget(host, requestPath string) routeTarget {
	cleaned := path.Clean("/" + requestPath)
	segments := pathSegments(cleaned)
	return routeTarget{
		Kind:        routeCustomDomain,
		Host:        host,
		RequestPath: requestPath,
		PageSlug:    pageSlugFromSegments(segments, 0),
	}
}

func normalizeCustomDomainFunnelPageSlug(requestPath string, funnelSlug string) string {
	p := strings.Trim(requestPath, "/")

	if p == "" {
		return "home"
	}

	if p == funnelSlug {
		return "home"
	}

	prefix := funnelSlug + "/"
	if strings.HasPrefix(p, prefix) {
		p = strings.TrimPrefix(p, prefix)
	}

	if p == "" {
		return "home"
	}

	return p
}

func pathSegments(cleanPath string) []string {
	parts := strings.Split(cleanPath, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return nil
		}
		out = append(out, part)
	}
	return out
}

func pageSlugFromSegments(segments []string, start int) string {
	if len(segments) <= start {
		return "index"
	}
	return strings.Join(segments[start:], "/")
}
