package dbsites

import (
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

func init() {
	httpcaddyfile.RegisterHandlerDirective("db_sites", parseCaddyfile)
}

func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var m Handler
	if err := m.UnmarshalCaddyfile(h.Dispenser); err != nil {
		return nil, err
	}
	return &m, nil
}

func (h *Handler) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	d.Next()
	for d.NextBlock(0) {
		switch d.Val() {
		case "database_url":
			if !d.NextArg() {
				return d.ArgErr()
			}
			h.DatabaseURL = d.Val()
		case "schema":
			if !d.NextArg() {
				return d.ArgErr()
			}
			h.Schema = d.Val()
		case "cache_ttl":
			if !d.NextArg() {
				return d.ArgErr()
			}
			dur, err := caddy.ParseDuration(d.Val())
			if err != nil {
				return d.Errf("invalid cache_ttl: %v", err)
			}
			h.CacheTTL = caddy.Duration(dur)
		case "negative_cache_ttl":
			if !d.NextArg() {
				return d.ArgErr()
			}
			dur, err := caddy.ParseDuration(d.Val())
			if err != nil {
				return d.Errf("invalid negative_cache_ttl: %v", err)
			}
			h.NegCacheTTL = caddy.Duration(dur)
		case "cache_clear_path":
			if !d.NextArg() {
				return d.ArgErr()
			}
			h.CacheClearPath = d.Val()
		case "cache_control":
			if !d.NextArg() {
				return d.ArgErr()
			}
			h.CacheControl = d.Val()
		default:
			return d.Errf("unknown subdirective %q", d.Val())
		}
	}
	return nil
}

var _ caddyfile.Unmarshaler = (*Handler)(nil)
