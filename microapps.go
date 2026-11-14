package dbsites

// Micro Saas (App Builder) apps on custom domains, in parallel with funnels.
//
// resolve_caddy_app(host) returns a row only when the host is linked to an app
// (app_builder_domains), so every other host falls through to the funnel
// resolver exactly as before. An app is one single-page document: every path on
// its domain gets the same published HTML and the app does its own routing.
// Merge tokens are NOT applied: app HTML carries a JS bundle where "{{" is code.

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

const routeMicroApp routeKind = "micro_app"

const microAppRouteRPC = `SELECT app_id, app_status, domain_status, purpose, sub_account_id, publish_id, html, updated_at FROM resolve_caddy_app($1)`

type microAppRow struct {
	AppID        string
	AppStatus    string
	DomainStatus string
	Purpose      string
	SubAccountID string
	PublishID    sql.NullString
	HTML         sql.NullString
	UpdatedAt    sql.NullTime
}

// lookupMicroApp says whether host belongs to a Micro Saas app. handled=false means
// "not an app domain": the caller continues with the funnel path. With handled=true,
// either site is set (serve it) or code is the error status to return.
func (h *Handler) lookupMicroApp(ctx context.Context, host string) (site *publishedSite, handled bool, code int, err error) {
	key := string(routeMicroApp) + "|" + host
	if cached, found, hit, cachedCode := h.cache.get(key); hit {
		if found {
			return cached, true, http.StatusOK, nil
		}
		// cachedCode 0 = remembered "not an app domain".
		return nil, cachedCode != 0, cachedCode, nil
	}

	var row microAppRow
	err = h.pool.QueryRow(ctx, microAppRouteRPC, host).Scan(
		&row.AppID, &row.AppStatus, &row.DomainStatus, &row.Purpose,
		&row.SubAccountID, &row.PublishID, &row.HTML, &row.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		h.cache.set(key, nil, false, h.negativeTTL(), 0)
		return nil, false, 0, nil
	}
	if err != nil {
		return nil, false, 0, fmt.Errorf("query micro app (resolve_caddy_app): %w", err)
	}

	code = microAppStatus(row)
	h.logger.Info("db_sites micro app resolved",
		zap.String("host", host),
		zap.String("app_id", row.AppID),
		zap.String("app_status", row.AppStatus),
		zap.String("domain_status", row.DomainStatus),
		zap.String("purpose", row.Purpose),
		zap.String("publish_id", row.PublishID.String),
		zap.Int("status", code),
	)
	if code != http.StatusOK {
		h.cache.set(key, nil, false, h.negativeTTL(), code)
		return nil, true, code, nil
	}

	site = &publishedSite{
		HTML:         row.HTML.String,
		Slug:         "app--" + row.AppID,
		ResolvedVia:  routeMicroApp,
		SubAccountID: row.SubAccountID,
		CacheETag:    weakETag(row.HTML.String),
	}
	if row.UpdatedAt.Valid {
		site.UpdatedAt = row.UpdatedAt.Time
	}
	h.cache.set(key, site, true, resolveTTL(time.Duration(h.CacheTTL)), http.StatusOK)
	return site, true, http.StatusOK, nil
}

// microAppStatus maps the resolver row to the same codes funnels use:
// 403 domain not verified / not a Micro Saas domain, 423 app not published, 404 nothing to serve.
func microAppStatus(row microAppRow) int {
	if row.DomainStatus != "verified" || row.Purpose != "micro_saas" {
		return http.StatusForbidden
	}
	if row.AppStatus != "published" {
		return http.StatusLocked
	}
	if !row.HTML.Valid || row.HTML.String == "" {
		return http.StatusNotFound
	}
	return http.StatusOK
}

func (h *Handler) negativeTTL() time.Duration {
	if ttl := time.Duration(h.NegCacheTTL); ttl > 0 {
		return ttl
	}
	return resolveTTL(time.Duration(h.CacheTTL))
}
