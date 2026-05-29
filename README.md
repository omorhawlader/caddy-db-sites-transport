# caddy-db-sites-transport

A Caddy v2 HTTP handler for the Website Builder dedicated serving plane.

The handler resolves an incoming custom domain request to a row in `published_sites`, reads the published `html_content`, and returns it directly as `text/html`. It follows the selected architecture option from the decision document: customer website traffic runs on a dedicated Caddy cluster, separate from the portal/control plane.

## Routing supported

- Custom domain: `https://example.com/about`
  - Requires `platform_domains.status = 'verified'`
  - Requires `platform_domains.purpose = 'funnel'` or `NULL`
  - Uses `site_funnels.domain_id -> platform_domains.id`

Root/homepage requests use page slug `index`, so `https://example.com/` resolves to the published row for that domain and `sf.slug || '--index'`.

## Installation With xcaddy

Install Go and `xcaddy` first:

```bash
go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
```

Build Caddy with this plugin from the local checkout:

```bash
xcaddy build --with github.com/thetestcoder/caddy-db-sites-transport=./
```

Or build directly from the GitHub module:

```bash
xcaddy build --with github.com/thetestcoder/caddy-db-sites-transport
```

Install the generated binary:

```bash
sudo mv ./caddy /usr/local/bin/caddy
sudo chmod +x /usr/local/bin/caddy
```

Verify:

```bash
caddy list-modules | grep db_sites
# http.handlers.db_sites
```

## Caddyfile

```caddyfile
{
	order db_sites before respond
}

:443 {
	tls /path/to/fullchain.pem /path/to/privkey.pem

	route {
		db_sites {
			database_url "postgres://user:pass@db.example.com:5432/sitebuilder"
			cache_ttl 30m
			negative_cache_ttl 2m
			cache_control "public, max-age=60"
		}
	}
}
```

## Environment variables

| Variable | Description |
|---|---|
| `DB_SITES_DATABASE_URL` | Postgres connection URL |
| `DB_SITES_CACHE_TTL` | Positive cache TTL, default `30m` |
| `DB_SITES_NEGATIVE_CACHE_TTL` | Not-found cache TTL, default same as positive TTL |
| `DB_SITES_CACHE_CLEAR_PATH` | Cache clear endpoint, default `/db-sites/cache/clear` |
| `DB_SITES_CACHE_CONTROL` | Optional `Cache-Control` header for HTML responses |

## Database assumptions

Published HTML is served from `published_sites.html_content`.

Custom domain routing resolves pages with:

```sql
published_sites.slug = site_funnels.slug || '--' || '{page-slug}'
```

Custom domain routing validates the domain via:

```sql
site_funnels.domain_id = platform_domains.id
platform_domains.status = 'verified'
platform_domains.purpose IN ('funnel', NULL)
```

Only `site_funnels.status = 'published'` and non-null published HTML are served.

## Operational endpoint

Clear the in-memory route/page cache:

```bash
curl -X POST https://site-serving.example.com/db-sites/cache/clear
```

## Status behavior

- `200 OK`: Published HTML found.
- `304 Not Modified`: Client `If-None-Match` matches the generated ETag.
- `404 Not Found`: Site/page/domain does not resolve.
- `423 Locked`: Custom domain is linked to a site, but `site_funnels.status != 'published'`.
- `403 Forbidden`: Custom domain exists but is not verified, or its `purpose` is not `funnel`/`NULL`.

## License

MIT
