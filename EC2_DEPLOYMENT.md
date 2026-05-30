# EC2 Deployment

This guide deploys the dedicated Website Builder serving plane on an EC2 instance using Caddy built with the `db_sites` plugin. The plugin reads published HTML from PostgreSQL.

## 1. Provision EC2

Recommended starting point:

- Ubuntu 22.04 or 24.04 LTS
- Security group inbound: `80/tcp`, `443/tcp`, and restricted `22/tcp`
- Security group outbound: allow Postgres access to your database host
- IAM role: no special permissions are required unless you add external secret management later

Point customer domains to the EC2 public IP or, preferably, to a load balancer in front of the EC2 instance.

## 2. Install Dependencies

```bash
sudo apt-get update
sudo apt-get install -y curl git tar postgresql-client
```

Install Go:

```bash
curl -LO https://go.dev/dl/go1.21.13.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.21.13.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.profile
. ~/.profile
go version
```

Install `xcaddy`. The `go install` command places the binary in `$HOME/go/bin`, which was added to `PATH` above:

```bash
go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
xcaddy version
```

## 3. Build Caddy With The Plugin Using xcaddy

Use one of the following build methods.

If you use the `storage s3` block for shared certificate storage, include your S3 storage module in the same `xcaddy build` command. Keep the storage plugin you already use in production.

### Option A: Build From Local Checkout

Clone this repository and build from the local directory:

```bash
git clone https://github.com/thetestcoder/caddy-db-sites-transport.git
cd caddy-db-sites-transport
xcaddy build --with github.com/thetestcoder/caddy-db-sites-transport=./
```

With S3 certificate storage:

```bash
xcaddy build \
  --with github.com/thetestcoder/caddy-db-sites-transport=./ \
  --with <your-caddy-s3-storage-module>
```

### Option B: Build Directly From GitHub

Use this when you do not need a local checkout on the EC2 instance:

```bash
xcaddy build --with github.com/thetestcoder/caddy-db-sites-transport
```

With S3 certificate storage:

```bash
xcaddy build \
  --with github.com/thetestcoder/caddy-db-sites-transport \
  --with <your-caddy-s3-storage-module>
```

Install the generated Caddy binary:

```bash
sudo mv ./caddy /usr/local/bin/caddy
sudo chmod +x /usr/local/bin/caddy
caddy version
caddy list-modules | grep db_sites
```

Expected module:

```text
http.handlers.db_sites
```

## 4. Create Caddy User And Directories

```bash
sudo groupadd --system caddy || true
sudo useradd --system \
  --gid caddy \
  --create-home \
  --home-dir /var/lib/caddy \
  --shell /usr/sbin/nologin \
  caddy || true

sudo mkdir -p /etc/caddy /var/log/caddy
sudo chown -R caddy:caddy /var/lib/caddy /var/log/caddy
```

## 5. Configure Environment

Create `/etc/caddy/db-sites.env`:

```bash
sudo tee /etc/caddy/db-sites.env >/dev/null <<'EOF'
DB_SITES_DATABASE_URL=postgres://USER:PASSWORD@DB_HOST:5432/DB_NAME?sslmode=require
DB_SITES_SCHEMA=public
DB_SITES_CACHE_TTL=30m
DB_SITES_NEGATIVE_CACHE_TTL=2m
DB_SITES_CACHE_CONTROL=public, max-age=60
EOF

sudo chmod 640 /etc/caddy/db-sites.env
sudo chown root:caddy /etc/caddy/db-sites.env
```

Use `sslmode=require` for managed PostgreSQL unless your database requires a different TLS mode.

Verify PostgreSQL connectivity from the EC2 instance:

```bash
source /etc/caddy/db-sites.env
psql "$DB_SITES_DATABASE_URL" -c "select now();"
psql "$DB_SITES_DATABASE_URL" -c "select current_database(), current_schema(), current_setting('search_path');"
```

## 6. Configure Caddy

Create `/etc/caddy/Caddyfile`:

```bash
sudo tee /etc/caddy/Caddyfile >/dev/null <<'EOF'
{
	order db_sites before respond

	storage s3 {
		bucket "prod-ssl-assets"
		host "prod-ssl-assets.s3.ap-south-1.amazonaws.com"
		prefix "certs/"
		use_iam_provider "true"
	}

	on_demand_tls {
		ask https://myappzbackend.com/functions/v1/white-label-validate-domain
	}

	acme_ca https://acme.zerossl.com/v2/DV90
	acme_eab {
		key_id ZERO_SSL_EAB_KEY_ID
		mac_key ZERO_SSL_EAB_MAC_KEY
	}

	ocsp_stapling off
	auto_https off
	email dev@searchy.in
}

http:// {
	redir https://{http.request.host}{http.request.uri} 301
}

https:// {
	tls {
		on_demand
		ciphers TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384 TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384 TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256 TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
		protocols tls1.2 tls1.3
	}

	@health path /health
	handle @health {
		respond "OK" 200 {
			close
		}
	}

	metrics /custom-metrics

	header Cache-Control "public, max-age=0, must-revalidate"

handle {
	db_sites
}
}
EOF

sudo caddy fmt --overwrite /etc/caddy/Caddyfile
```

This replaces the previous S3 website `reverse_proxy`/`transport aws` blocks. S3 remains only for certificate storage through `storage s3`; page HTML is served by `db_sites` from the database.

Replace `ZERO_SSL_EAB_KEY_ID`, `ZERO_SSL_EAB_MAC_KEY`, and `<your-caddy-s3-storage-module>` with your real production values. Do not commit those secrets to this repository.

The `db_sites` handler logs each request step, including host normalization, page slug resolution, cache state, SQL lookup status, and detailed 404 diagnostics. Watch logs while testing a domain:

```bash
sudo journalctl -u caddy -f
```

## 7. Configure Systemd

Create `/etc/systemd/system/caddy.service`:

```bash
sudo tee /etc/systemd/system/caddy.service >/dev/null <<'EOF'
[Unit]
Description=Caddy Website Builder Serving Plane
Documentation=https://caddyserver.com/docs/
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
User=caddy
Group=caddy
EnvironmentFile=/etc/caddy/db-sites.env
ExecStart=/usr/local/bin/caddy run --environ --config /etc/caddy/Caddyfile
ExecReload=/usr/local/bin/caddy reload --config /etc/caddy/Caddyfile --force
TimeoutStopSec=5s
LimitNOFILE=1048576
PrivateTmp=true
ProtectSystem=full
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF
```

Start Caddy:

```bash
sudo systemctl daemon-reload
sudo systemctl enable caddy
sudo systemctl start caddy
sudo systemctl status caddy --no-pager
```

## 8. DNS And Domain Requirements

For a custom domain to serve, PostgreSQL must contain matching rows:

- DNS must point to this serving plane.
- `platform_domains.domain` must match the request host.
- `platform_domains.status` must be `verified`.
- `platform_domains.purpose` must be `funnel` or `NULL`.
- `site_funnels.platform_domain_id` must point to the `platform_domains.id`.
- `site_funnels.status` must be `published`.
- `published_sites.slug` must equal `site_funnels.slug || '--' || page_slug`.
- `published_sites.html_content` must not be `NULL`.
- The tables must be in `DB_SITES_SCHEMA`, default `public`.

Homepage requests use page slug `index`.

## 9. Verify Deployment

Check service logs:

```bash
sudo journalctl -u caddy -f
```

Test from the EC2 instance:

```bash
curl -I -H 'Host: example.com' http://127.0.0.1/
curl -H 'Host: example.com' http://127.0.0.1/ | head
curl -I -H 'Host: example.com' http://127.0.0.1/about
```

Expected successful response:

```text
HTTP/1.1 200 OK
Content-Type: text/html; charset=utf-8
X-Site-Resolved-Via: custom_domain
```

Clear the in-memory cache after changing domain/site mappings:

```bash
curl -X POST -H 'Host: example.com' http://127.0.0.1/db-sites/cache/clear
```

## 10. Troubleshooting

Check loaded module:

```bash
caddy list-modules | grep db_sites
```

Validate Caddy config:

```bash
sudo caddy validate --config /etc/caddy/Caddyfile
```

Common responses:

- `403 Forbidden`: domain exists but is not verified, or `purpose` is not `funnel`/`NULL`.
- `404 Not Found`: domain/page does not resolve to a published HTML row.
- `423 Locked`: domain is linked to a site, but the funnel is not published.
- `502 Bad Gateway`: Caddy cannot query Postgres or the query failed.

For `502`, check:

- `DB_SITES_DATABASE_URL` is correct.
- EC2 can reach the PostgreSQL security group on port `5432`.
- PostgreSQL requires the configured `sslmode`.
- Tables and columns match the expected schema.

## 11. Updating The Plugin

If the repository is cloned on the EC2 instance:

```bash
cd caddy-db-sites-transport
git pull
xcaddy build --with github.com/thetestcoder/caddy-db-sites-transport=./
```

If building directly from GitHub:

```bash
xcaddy build --with github.com/thetestcoder/caddy-db-sites-transport
```

If your Caddyfile uses `storage s3`, include the S3 storage module again during updates:

```bash
xcaddy build \
  --with github.com/thetestcoder/caddy-db-sites-transport \
  --with <your-caddy-s3-storage-module>
```

Then replace the running binary:

```bash
sudo systemctl stop caddy
sudo mv ./caddy /usr/local/bin/caddy
sudo chmod +x /usr/local/bin/caddy
sudo systemctl start caddy
caddy list-modules | grep db_sites
```
