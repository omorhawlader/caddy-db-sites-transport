package dbsites

import "testing"

func TestResolveTargetCustomDomain(t *testing.T) {
	target := resolveTarget("example.com", "/")
	if target.Kind != routeCustomDomain || target.Host != "example.com" || target.PageSlug != "index" {
		t.Fatalf("unexpected target: %#v", target)
	}
}

func TestResolveTargetCustomDomainPage(t *testing.T) {
	target := resolveTarget("example.com", "/about")
	if target.Kind != routeCustomDomain || target.Host != "example.com" || target.PageSlug != "about" {
		t.Fatalf("unexpected target: %#v", target)
	}
}

func TestWeakETagStable(t *testing.T) {
	a := weakETag("<html>ok</html>")
	b := weakETag("<html>ok</html>")
	if a == "" || a != b {
		t.Fatalf("etag should be stable, got %q and %q", a, b)
	}
}
