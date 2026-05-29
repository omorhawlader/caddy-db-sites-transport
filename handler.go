package dbsites

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func init() {
	caddy.RegisterModule(new(Handler))
}

const defaultCacheClearPath = "/db-sites/cache/clear"

type Handler struct {
	DatabaseURL string `json:"database_url,omitempty"`

	CacheTTL    caddy.Duration `json:"cache_ttl,omitempty"`
	NegCacheTTL caddy.Duration `json:"negative_cache_ttl,omitempty"`

	CacheClearPath string `json:"cache_clear_path,omitempty"`
	CacheControl   string `json:"cache_control,omitempty"`

	pool   *pgxpool.Pool
	cache  *responseCache
	logger *zap.Logger
}

type publishedSite struct {
	Title       string
	HTML        string
	Slug        string
	FunnelType  string
	UpdatedAt   time.Time
	CacheETag   string
	ResolvedVia routeKind
}

func (*Handler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.db_sites",
		New: func() caddy.Module { return new(Handler) },
	}
}

func (h *Handler) Provision(ctx caddy.Context) error {
	h.logger = ctx.Logger()
	h.loadEnvDefaults()
	if h.DatabaseURL == "" {
		return fmt.Errorf("db_sites: database_url is required")
	}
	if h.CacheClearPath == "" {
		h.CacheClearPath = defaultCacheClearPath
	}

	pool, err := pgxpool.New(context.Background(), h.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db_sites: connect to database: %w", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		return fmt.Errorf("db_sites: ping database: %w", err)
	}
	h.pool = pool
	h.cache = newResponseCache()
	h.logger.Info("db_sites provisioned",
		zap.String("database_url", redactDatabaseURL(h.DatabaseURL)),
		zap.Duration("cache_ttl", time.Duration(h.CacheTTL)),
	)
	return nil
}

func (h *Handler) loadEnvDefaults() {
	envOr := func(field *string, key string) {
		if *field == "" {
			*field = os.Getenv(key)
		}
	}
	envOr(&h.DatabaseURL, "DB_SITES_DATABASE_URL")
	envOr(&h.CacheClearPath, "DB_SITES_CACHE_CLEAR_PATH")
	envOr(&h.CacheControl, "DB_SITES_CACHE_CONTROL")

	if h.CacheTTL == 0 {
		if s := os.Getenv("DB_SITES_CACHE_TTL"); s != "" {
			if d, err := caddy.ParseDuration(s); err == nil {
				h.CacheTTL = caddy.Duration(d)
			}
		}
	}
	if h.NegCacheTTL == 0 {
		if s := os.Getenv("DB_SITES_NEGATIVE_CACHE_TTL"); s != "" {
			if d, err := caddy.ParseDuration(s); err == nil {
				h.NegCacheTTL = caddy.Duration(d)
			}
		}
	}
}

func (h *Handler) Validate() error {
	if h.pool == nil || h.cache == nil {
		return fmt.Errorf("db_sites: not properly provisioned")
	}
	return nil
}

func (h *Handler) Cleanup() error {
	if h.pool != nil {
		h.pool.Close()
	}
	return nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	host := effectiveRequestHost(r)
	if host == "" {
		return caddyhttp.Error(http.StatusBadRequest, fmt.Errorf("missing Host header"))
	}
	if strings.EqualFold(pathClean(r.URL.Path), pathClean(h.CacheClearPath)) {
		return h.serveCacheClear(w, r)
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		return caddyhttp.Error(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
	}

	target := resolveTarget(host, r.URL.Path)

	site, found, code, err := h.lookupPublishedSite(r.Context(), target)
	if err != nil {
		h.logger.Error("published site lookup failed", zap.String("host", host), zap.Error(err))
		return caddyhttp.Error(http.StatusBadGateway, err)
	}
	if !found {
		return caddyhttp.Error(code, fmt.Errorf(http.StatusText(code)))
	}

	writeHTML(w, r, site, h.CacheControl)
	return nil
}

func (h *Handler) lookupPublishedSite(ctx context.Context, target routeTarget) (*publishedSite, bool, int, error) {
	key := cacheKey(target)
	if site, found, hit, code := h.cache.get(key); hit {
		return site, found, code, nil
	}

	site, found, code, err := h.queryPublishedSite(ctx, target)
	if err != nil {
		return nil, false, 0, err
	}
	if found {
		h.cache.set(key, site, true, resolveTTL(time.Duration(h.CacheTTL)), http.StatusOK)
		return site, true, http.StatusOK, nil
	}

	ttl := time.Duration(h.NegCacheTTL)
	if ttl <= 0 {
		ttl = resolveTTL(time.Duration(h.CacheTTL))
	}
	h.cache.set(key, nil, false, ttl, code)
	return nil, false, code, nil
}

func (h *Handler) queryPublishedSite(ctx context.Context, target routeTarget) (*publishedSite, bool, int, error) {
	var row pgx.Row
	switch target.Kind {
	case routeCustomDomain:
		row = h.pool.QueryRow(ctx, customDomainSQL, target.Host, target.PageSlug)
	default:
		return nil, false, http.StatusNotFound, fmt.Errorf("unknown route kind %q", target.Kind)
	}

	var site publishedSite
	site.ResolvedVia = target.Kind
	if err := row.Scan(&site.Slug, &site.Title, &site.FunnelType, &site.HTML, &site.UpdatedAt); err != nil {
		if err == pgx.ErrNoRows {
			code, err := h.notFoundStatus(ctx, target)
			return nil, false, code, err
		}
		return nil, false, 0, fmt.Errorf("query published site: %w", err)
	}
	site.CacheETag = weakETag(site.HTML)
	return &site, true, http.StatusOK, nil
}

func (h *Handler) notFoundStatus(ctx context.Context, target routeTarget) (int, error) {
	if target.Kind == routeCustomDomain {
		return h.customDomainMissStatus(ctx, target.Host)
	}
	return http.StatusNotFound, nil
}

func (h *Handler) customDomainMissStatus(ctx context.Context, host string) (int, error) {
	var domainStatus, purpose string
	var siteStatus sql.NullString
	err := h.pool.QueryRow(ctx, customDomainStatusSQL, host).Scan(&domainStatus, &purpose, &siteStatus)
	if err == pgx.ErrNoRows {
		return http.StatusNotFound, nil
	}
	if err != nil {
		return 0, fmt.Errorf("query custom domain status: %w", err)
	}
	if domainStatus != "verified" || (purpose != "" && purpose != "funnel") {
		return http.StatusForbidden, nil
	}
	if siteStatus.Valid && siteStatus.String != "published" {
		return http.StatusLocked, nil
	}
	return http.StatusNotFound, nil
}

func (h *Handler) serveCacheClear(w http.ResponseWriter, r *http.Request) error {
	switch r.Method {
	case http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodHead:
	default:
		w.Header().Set("Allow", "GET, HEAD, POST, DELETE")
		return caddyhttp.Error(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
	}
	removed := h.cache.invalidateAll()
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return nil
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(struct {
		OK      bool `json:"ok"`
		Removed int  `json:"removed"`
	}{OK: true, Removed: removed})
	return nil
}

func writeHTML(w http.ResponseWriter, r *http.Request, site *publishedSite, cacheControl string) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("ETag", site.CacheETag)
	h.Set("X-Site-Slug", site.Slug)
	h.Set("X-Site-Resolved-Via", string(site.ResolvedVia))
	if cacheControl != "" {
		h.Set("Cache-Control", cacheControl)
	}
	if match := r.Header.Get("If-None-Match"); match != "" && match == site.CacheETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(site.HTML))
	}
}

func cacheKey(target routeTarget) string {
	return string(target.Kind) + "|" + target.Host + "|" + target.PageSlug
}

func weakETag(s string) string {
	sum := sha256.Sum256([]byte(s))
	return `W/"` + hex.EncodeToString(sum[:12]) + `"`
}

func redactDatabaseURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(invalid database_url)"
	}
	return u.Redacted()
}

func pathClean(p string) string {
	if p == "" {
		return "/"
	}
	return "/" + strings.TrimPrefix(strings.TrimSuffix(p, "/"), "/")
}

const customDomainSQL = `
SELECT
	ps.slug,
	COALESCE(ps.title, ''),
	COALESCE(ps.funnel_type, ''),
	ps.html_content,
	ps.updated_at
FROM published_sites ps
JOIN site_funnels sf ON sf.id = ps.funnel_id
JOIN platform_domains pd ON pd.id = sf.domain_id
WHERE (lower(pd.domain) = lower($1) OR lower(ps.custom_domain) = lower($1) OR lower(sf.domain) = lower($1))
  AND pd.status = 'verified'
  AND (pd.purpose = 'funnel' OR pd.purpose IS NULL)
  AND sf.status = 'published'
  AND ps.slug = sf.slug || '--' || $2
  AND ps.html_content IS NOT NULL
LIMIT 1`

const customDomainStatusSQL = `
SELECT
	pd.status,
	COALESCE(pd.purpose, ''),
	sf.status
FROM platform_domains pd
LEFT JOIN site_funnels sf ON sf.domain_id = pd.id
WHERE lower(pd.domain) = lower($1)
LIMIT 1`

var (
	_ caddy.Module                = (*Handler)(nil)
	_ caddy.Provisioner           = (*Handler)(nil)
	_ caddy.Validator             = (*Handler)(nil)
	_ caddy.CleanerUpper          = (*Handler)(nil)
	_ caddyhttp.MiddlewareHandler = (*Handler)(nil)
)
