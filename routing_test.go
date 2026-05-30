package dbsites

import "testing"

func TestResolveTargetCustomDomain(t *testing.T) {
	target := resolveTarget("example.com", "/")
	if target.Kind != routeCustomDomain || target.Host != "example.com" || target.RequestPath != "/" || target.PageSlug != "index" {
		t.Fatalf("unexpected target: %#v", target)
	}
}

func TestResolveTargetCustomDomainPage(t *testing.T) {
	target := resolveTarget("example.com", "/about")
	if target.Kind != routeCustomDomain || target.Host != "example.com" || target.PageSlug != "about" {
		t.Fatalf("unexpected target: %#v", target)
	}
}

func TestNormalizeCustomDomainPageSlug(t *testing.T) {
	tests := map[string]string{
		"/":                           "home",
		"/home":                       "home",
		"/about":                      "about",
		"/test-funnel-domain":         "home",
		"/test-funnel-domain/":        "home",
		"/test-funnel-domain/home":    "home",
		"/test-funnel-domain/about":   "about",
		"test-funnel-domain/services": "services",
	}
	for input, want := range tests {
		got := normalizeCustomDomainPageSlug(input, "test-funnel-domain")
		if got != want {
			t.Fatalf("normalizeCustomDomainPageSlug(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWeakETagStable(t *testing.T) {
	a := weakETag("<html>ok</html>")
	b := weakETag("<html>ok</html>")
	if a == "" || a != b {
		t.Fatalf("etag should be stable, got %q and %q", a, b)
	}
}
