# Deployment

How to run KamaraPMS as it would run on a server, on one machine, and what has to be true when it moves to a server of your choice. Nothing here names a provider, a domain or a certificate authority: the same files run on a laptop, a VPS or a cloud instance that has Docker.

Status of this document: written with the production-like stack of `deploy/` and checked by running it (section 12 says what was run). It is **not** a statement that the system is production ready: backup and restore (section 9), monitoring and a rehearsed rollback are separate work (`architecture/19-post-implementation-audit.md`, F-04 and F-13).

## 1. Prerequisites

- Docker with Compose v2.24 or newer (the stack uses `!override` in the TLS file), about 2 GB of free memory and 1 GB of disk.
- A shell with `bash` and `curl` for the smoke test (Git Bash on Windows is enough).
- For a real server later: a host name you control, a way to get a certificate (section 7), a firewall that opens 80 and 443 only, and a place for backups that is not the same disk.

The images are built from the repository, so no registry is needed. The base images are `golang:1.26-trixie`, `node:22-alpine`, `nginxinc/nginx-unprivileged:1.27-alpine` and `gcr.io/distroless/static-debian12:nonroot`; the PostgreSQL image is `postgres:16-alpine`. All are overridable build arguments (see the top of the `Dockerfile`), and a digest can be given instead of a tag when you want a build that never moves.

## 2. Local production-like deployment

```text
browser ──▶ proxy (nginx: the page, /api) ──▶ api (Go) ──▶ db (PostgreSQL)
               only this port is published        private network, no published port
```

```bash
cp deploy/.env.example deploy/.env
# edit deploy/.env: set POSTGRES_PASSWORD and PMS_JWT_SECRET (the file says how to generate them)

docker compose -f deploy/compose.yaml up -d --build      # builds the two images, starts db, runs the migrations, starts api, then the proxy
docker compose -f deploy/compose.yaml ps                  # db, api and proxy healthy; migrate exited (0)

# the first tenant and administrator (the password is printed once)
docker compose -f deploy/compose.yaml run --rm --no-deps --entrypoint /usr/local/bin/pms-admin api \
  create-tenant -code DEMO -name "Demo Hotels" -timezone Asia/Jakarta
docker compose -f deploy/compose.yaml run --rm --no-deps --entrypoint /usr/local/bin/pms-admin api \
  create-admin -tenant DEMO -email you@example.com -name "Your Name"

# open http://127.0.0.1:8080 (the port is PMS_PUBLIC_PORT) and sign in with tenant code DEMO
scripts/prod-smoke.sh                                      # the checks of section 12
docker compose -f deploy/compose.yaml down                 # stops it; the data stays in the volume. Add -v to delete the data too.
```

On Git Bash for Windows, put `MSYS_NO_PATHCONV=1` in front of the commands that have `--entrypoint /usr/...`, or the path is rewritten.

The stack has a project name of its own (`kamarapms-deploy`), so its containers, network and volume (`kamarapms-deploy_pms-db`) never touch the development database of the root `compose.yaml` (project `kamarapms`, volume `kamarapms_pms-db`, port 55432). They can run side by side.

What is in the stack:

| Service | Image | What it does | Published |
|---|---|---|---|
| `db` | `postgres:16-alpine` | the database, in the volume `pms-db` | no |
| `migrate` | `kamarapms-api` | a job: applies the migrations of this release and ends | no |
| `api` | `kamarapms-api` (44 MB, user `nonroot`, read-only file system, no capabilities) | the API, port 8080 inside | no |
| `proxy` | `kamarapms-web` (50 MB, user `nginx`, read-only file system) | serves the single-page app, forwards `/api`, `/healthz`, `/readyz` | `PMS_BIND:PMS_PUBLIC_PORT` (default `127.0.0.1:8080`) |

## 3. Environment variables

The API reads its settings from the environment (`internal/platform/config`); nothing is read from a file in the image, and no secret is in an image. In the stack, `deploy/.env` (ignored by git) feeds the compose file, which passes each value to the container that needs it.

| Variable | Default | Meaning |
|---|---|---|
| `PMS_ENV` | `development` (the image sets `production`) | `production` makes the application refuse an insecure cookie setting and logs JSON by default |
| `PMS_HTTP_ADDR` | `:8080` | the address the API listens on, inside the container |
| `PMS_DATABASE_URL` | none, required | `postgres://user:password@host:5432/db?sslmode=...`. The compose file builds it from `POSTGRES_*`. With an external database use `sslmode=require` or `verify-full`. The migration job needs this variable and no other |
| `PMS_JWT_SECRET` | none, required | at least 32 characters (`openssl rand -base64 48`). Signs access tokens; changing it signs everybody out |
| `PMS_ACCESS_TOKEN_TTL`, `PMS_REFRESH_TOKEN_TTL` | `15m`, `720h` | session lifetimes (the access token cannot exceed 1 h) |
| `PMS_COOKIE_SECURE` | `true` outside development | the refresh cookie is sent over https only; `false` is refused in production. A browser accepts it from `http://localhost` too, which is why the stack can be tried without a certificate |
| `PMS_TRUSTED_PROXIES` | empty | the addresses or CIDR ranges of the reverse proxies whose `X-Forwarded-For` is believed (section 6.3). Empty trusts nobody. `0.0.0.0/0` is refused. The compose file gives the proxy the fixed address `172.29.0.10` and trusts exactly that |
| `PMS_RATE_LIMIT_PER_MINUTE` | `600` | requests a minute per client address, burst equal to it; `0` turns it off. Counted per real client (section 6.3). Health probes are exempt |
| `PMS_DB_MAX_CONNS`, `PMS_DB_LOCK_TIMEOUT` | `10`, `5s` | the pool, and how long a transaction waits for a row lock before `409 RESOURCE_BUSY` |
| `PMS_SHUTDOWN_TIMEOUT` | `15s` | how long the API lets requests in flight finish after SIGTERM (the compose `stop_grace_period` is 30 s) |
| `PMS_LOG_LEVEL`, `PMS_LOG_FORMAT` | `info`, `json` in production | `debug`, `info`, `warn`, `error`; `json` or `text` |
| `PMS_MIGRATE_ON_START` | `false` | leave it false: migrations are a job (section 5) |
| `PMS_SMTP_*` | empty (e-mail off) | the mail server for the reservation confirmation; credentials are refused over an unencrypted connection in production |
| `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD` | `pms`, `pms`, required | the PostgreSQL container of the stack |
| `PMS_BIND`, `PMS_PUBLIC_PORT`, `PMS_PUBLIC_TLS_PORT`, `PMS_VERSION` | `127.0.0.1`, `8080`, `8443`, `local` | where the proxy listens at the host, and the tag of the images |

**CORS.** None, on purpose. The page and the API are one origin (the proxy), the refresh cookie is `SameSite=Strict` and scoped to `/api/v1/auth`, and the API sends no `Access-Control-*` header. A page served from another origin would be refused by the browser; supporting that would need an explicit allow-list and a change of the cookie policy, and is not offered.

**Frontend API URL.** The single-page app calls `/api/...` on the origin it was loaded from. There is no build-time URL to configure; where the API is (host and port) is the `API_UPSTREAM` variable of the proxy container (default `api:8080`, the compose service). Moving the API means changing that one variable, not rebuilding the page.

**Secrets.** Never in an image, never in git (`.env` and `deploy/.env` are ignored; only the `.example` files are tracked). On a server give the compose file its values from the environment of the machine or from the secret store of your platform; the compose file uses `${VAR:?message}` for the two that are required, so it refuses to start without them.

## 4. Database setup

The stack starts PostgreSQL with the volume `pms-db`. To use a database you run yourself, remove the `db` service and the `depends_on` on it from the compose file, and give `PMS_DATABASE_URL` of the `api` and `migrate` services. Requirements: PostgreSQL 16 or 18 (both pass the schema tests in CI), a database and a user that owns it (the migrations create tables, functions, triggers and views), UTF-8.

The application connects as that one user. It needs no superuser and no extension.

## 5. Migration

```text
start PostgreSQL ─▶ run migrations (job) ─▶ verify the schema ─▶ start the application ─▶ health and readiness
```

In the stack this is the order of the compose file: `migrate` waits for `db` to be healthy, `api` waits for `migrate` to complete successfully, `proxy` waits for `api` to be healthy. To do it by hand:

```bash
docker compose -f deploy/compose.yaml up -d db
docker compose -f deploy/compose.yaml run --rm migrate            # prints "applied N migration(s)"
docker compose -f deploy/compose.yaml run --rm --entrypoint /usr/local/bin/migrate migrate version    # the schema version
docker compose -f deploy/compose.yaml run --rm --entrypoint /usr/local/bin/migrate migrate status     # every migration and its state
docker compose -f deploy/compose.yaml up -d
```

- **Several instances.** A migration that changes the schema holds a PostgreSQL session advisory lock while it runs, so jobs started together take turns: the second waits, finds nothing to apply and ends with success. This was checked with three jobs started at once on an empty database: one applied 62 migrations, the other two applied none, all three ended 0. Run the job **once per release, before the new instances start**; do not start instances with `PMS_MIGRATE_ON_START=true`.
- **A release that is ahead of the schema is not ready.** `/readyz` fails (503) while the database has fewer migrations than the binary knows, and succeeds when it has them all or more. A new binary started before its job finished is therefore never sent traffic, and an old binary keeps serving while a newer schema is in place (migrations are written to be compatible with the release before them).
- **Rolling back.** `migrate down` exists and migrations 00060 and 00062 refuse to roll back over data they could not represent, but the others drop what they added, so the way back for a populated database is a restore (section 9), not a down migration.

## 6. Reverse proxy

`deploy/nginx/default.conf.template` is the whole configuration of the proxy. It is a template: only `${API_UPSTREAM}` is substituted, and no domain is written in it (`server_name _`).

### 6.1 Routes

| Path | Goes to |
|---|---|
| `/api/` | the Go API |
| `/healthz`, `/readyz` | the Go API (so a monitor outside sees what a user sees; they answer a status word only) |
| `/assets/` | the built files of the page, cached for a year (their names carry a hash); a missing one is 404, not the page |
| anything else | `index.html`, so a deep link or a reload on `/reservations/12` works (history mode) |

There is no WebSocket or server-sent events in the application (searched the source), so nothing is upgraded.

### 6.2 Headers, limits, timeouts

- **Security headers**, on the page and on the API answers, set once by the proxy (`snippets/security-headers.conf`): `Content-Security-Policy` (`default-src 'self'`, no inline script, `frame-ancestors 'none'`; `style-src` allows `'unsafe-inline'` because the interface sets style attributes), `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy`, `Permissions-Policy`, `Cross-Origin-Opener-Policy`. The API sets `nosniff`, frame denial and `no-referrer` itself too, for the day it is reached without the proxy; the proxy drops those so an answer never carries two values. `Strict-Transport-Security` is set only in the TLS configuration (section 7).
- **Cache.** The page is `no-cache` (a new release is picked up at the next load); the API answers `no-store`.
- **Request size.** `client_max_body_size 2m`, a wall above the API's own limit of 1 MiB for a JSON body (a body between the two gets the API's clear error; above 2 MiB the proxy answers 413).
- **Timeouts.** Headers 15 s, body 30 s, connect 5 s, send 30 s, read 70 s: the API gives up on a request after 60 s, so the proxy never waits less. `keepalive` to the API is on.
- **Errors.** An answer of the API for an unexpected failure is the generic problem document with a request id; the detail is in the log only. The proxy tells no version (`server_tokens off`).

### 6.3 Trusted proxy and the real client address

The rate limit, the audit trail (`ip_address`), the sign-in and approval throttles and the access log all use the **client address**. Behind a proxy the connection comes from the proxy, so the address of the person is what the proxy wrote in `X-Forwarded-For`. That header can be forged by anybody, so the API follows these rules (`internal/platform/httpx/clientip.go`, with unit tests):

1. If the peer of the connection is **not** in `PMS_TRUSTED_PROXIES` (or the setting is empty), the client is the peer, and `X-Forwarded-For` is ignored, whatever it says, including the address of a trusted proxy.
2. If the peer is a trusted proxy, the list in `X-Forwarded-For` is read **from the right**: addresses that are themselves trusted proxies are other hops of ours and are skipped; the first address that is not trusted is the client. What stands to its left was written by the client or before our proxies and is never read. Ports and IPv6 brackets are accepted; garbage stops the walk; no header means the proxy itself.
3. No other header is read (`X-Real-IP`, `Forwarded`).

The proxy in this repository is the edge, so it **overwrites** `X-Forwarded-For` with the address of its own peer (`proxy_set_header X-Forwarded-For $remote_addr`) instead of appending to what the client sent. Together: a client that forges the header through the proxy has it replaced; a client that talks to the API directly has it ignored.

If a load balancer or a CDN stands in front of this proxy: trust it in nginx (`set_real_ip_from <its address or range>; real_ip_header X-Forwarded-For; real_ip_recursive on;` in `server`), keep the overwrite line (it then forwards the address nginx resolved), and leave `PMS_TRUSTED_PROXIES` as the proxy's own address. Do not put the load balancer's range in `PMS_TRUSTED_PROXIES` unless it talks to the API directly.

On Docker Desktop (Windows, macOS) a request from the host browser reaches the proxy through the NAT of the virtual machine and shows the address of the network gateway (`172.29.0.1` in the logs) rather than the address of the machine. On a Linux server the address of a real remote client is preserved. The smoke test therefore uses clients inside the network, each with an address of its own.

The login throttle and the rate limiter keep their counters in the memory of **one** process. The supported shape for the pilot is one API instance; with several, each instance counts on its own (the limits are multiplied) and a restart clears them.

## 7. TLS strategy

The application never speaks TLS itself. Pick one:

- **A. This proxy terminates TLS** (`deploy/compose.tls.yaml`): you provide `deploy/certs/tls.crt` (the certificate, with its chain) and `deploy/certs/tls.key`. The proxy serves https on 8443 (publish it as 443 on a server by setting `PMS_PUBLIC_TLS_PORT=443`, and `PMS_PUBLIC_PORT=80`), redirects http to https with 308 (to the default https port, so publish 80 and 443 on a server; the redirect of a trial on 8443 points at 443), enables TLS 1.2 and 1.3 only, and adds `Strict-Transport-Security`. The probes stay on http so that a load balancer can check them without a certificate. Certificates come from your authority; renew by replacing the two files and `docker compose ... exec proxy nginx -s reload`. Nothing in the stack requests or renews a certificate.
- **B. A TLS terminating load balancer or platform proxy in front** (most clouds have one): it forwards http to this proxy's port 8080; set `PMS_COOKIE_SECURE=true` (the default) because users still reach the site over https, and trust the balancer as in section 6.3.
- **C. A certificate manager next to the stack** (any ACME client that writes the two files): works with A.

For a trial on one machine: `deploy/make-dev-cert.sh` makes a self-signed certificate (30 days, `localhost`), then

```bash
docker compose -f deploy/compose.yaml -f deploy/compose.tls.yaml up -d --build
curl -k https://127.0.0.1:8443/readyz
```

A self-signed certificate is for trials only. HSTS tells browsers to refuse http for the host for a year: switch it on only for a host that will keep https.

## 8. Health and readiness

| Endpoint | Meaning | Looks at the database | Use it to |
|---|---|---|---|
| `GET /healthz` | **alive**: the process runs | never | decide to **restart** a container |
| `GET /readyz` | **ready**: send me traffic: the database answers and the schema has every migration of this release | yes, with a 2 s timeout | decide whether to **route** to a container |

A database that is down makes `/readyz` answer 503 `NOT_READY` and leaves `/healthz` at 200: restarting the API would not help. When the database is back, `/readyz` returns to 200 without a restart (checked). The answer says only that a dependency is not ready; the reason (the schema version, the database error) is in the log, at `warn`.

The image has no shell and no curl, so the container health check is the binary: `api healthcheck` (alive) and `api healthcheck -ready` (ready); it reads only `PMS_HTTP_ADDR`. The Dockerfile and the compose file use `-ready`, which is what gates the start of the proxy. An orchestrator that has separate probes (Kubernetes, Nomad) should use `-ready` or `/readyz` for readiness and `/healthz` for liveness. Probes are exempt from the rate limit and are logged at `debug` when they succeed.

Graceful shutdown: SIGTERM reaches the API as process 1; it stops accepting connections, lets requests in flight finish (`PMS_SHUTDOWN_TIMEOUT`) and exits 0. Checked: `docker compose stop api` ended in under a second with exit code 0 and the log line `shutting down`.

## 9. Backup dependency

All state is in PostgreSQL (the volume `pms-db`, or your external database): the images hold no data, and there are no uploaded files. **A backup of the database is therefore the backup of the system.** The procedure, the schedule and the recovery targets are a separate piece of work that is not done (audit F-04); until it is, this is the minimum, and it has been run once:

```bash
# a backup (custom format, compressed)
docker compose -f deploy/compose.yaml exec -T db pg_dump -U pms -Fc pms > pms-$(date +%F).dump

# a restore into an empty database (here a scratch one, to check the backup)
docker compose -f deploy/compose.yaml exec -T db psql -U pms -d postgres -c "CREATE DATABASE restore_check"
docker compose -f deploy/compose.yaml exec -T db pg_restore -U pms -d restore_check --no-owner < pms-2026-10-07.dump
```

Checked on the stack above: a dump of 0.7 MB restored with all 62 migrations, the tenant and the user. Keep dumps on another disk or machine, test a restore regularly, and decide how much data you can afford to lose (RPO) and how long you can be down (RTO) before the pilot starts.

## 10. Logs

Every container writes to standard output; Docker keeps 5 files of 10 MB per container (`json-file`, set in the compose file). The API logs JSON in production, one object a line: `time`, `level`, `msg`, and on a request `request_id`, `client_ip`, `method`, `route`, `path`, `status`, `bytes`, `duration_ms`. The proxy logs `$remote_addr "$request" $status ... rid=$request_id`.

- **Correlation.** The proxy generates a request id and passes it to the API as `X-Request-ID`; the API logs it and returns it in the answer and in every error document (`request_id`). A user who reports an error gives that id; `docker compose logs api | grep <id>` and the same id in the proxy line show both sides.
- **No secrets.** The database URL is logged with the password masked; passwords, tokens and approval credentials are not logged.
- A successful probe is logged at `debug`; an unexpected failure is logged at `error` with the cause, and the caller gets a generic problem document.

## 11. Troubleshooting

| Symptom | Likely cause | What to do |
|---|---|---|
| `docker compose up` stops with "set POSTGRES_PASSWORD in deploy/.env" | the required values are missing | copy `deploy/.env.example` to `deploy/.env` and fill them |
| `migrate` exits 1 with "password authentication failed" | the database volume was created with another password | the password of an existing volume does not change with the variable; use the original, or remove the volume (`down -v`, **deletes the data**) on a trial |
| `api` stays "starting", `/readyz` 503 | the schema is behind (the job failed or has not run), or the database is not reachable | `docker compose logs migrate api`; the `warn` line says which |
| `api` exits at start with "invalid configuration" | a required value is missing or refused (secret too short, `PMS_COOKIE_SECURE=false` in production, bad `PMS_TRUSTED_PROXIES`) | the message names the variable |
| every user gets 429 | the limit is counted on the proxy's address: `PMS_TRUSTED_PROXIES` does not contain the proxy | set it to the address of the proxy (the compose file does); the API logs a warning at start in production when it is empty |
| the page loads but every call is 502 | `API_UPSTREAM` is wrong, or the API is down | `docker compose logs proxy api` |
| sign-in works on `localhost` but not on a server | the refresh cookie is `Secure` and the site is on plain http | serve the site over https (section 7) |
| a deep link or a reload shows the proxy's 404 | a custom proxy without the fallback to `index.html` | use `deploy/nginx/default.conf.template` |
| the browser console says "Refused to ... Content Security Policy" | something loads from another origin | the page is supposed to load nothing from elsewhere; find the origin in the message |
| `docker compose` says the project already exists with other containers | a project name collision | the stack's name is `kamarapms-deploy`; do not change it to `kamarapms` (the development database) |
| two clients share one rate limit | the proxy in front of this one is not trusted by nginx | section 6.3, `set_real_ip_from` |

## 12. What was run to check this document

On the machine that wrote it (Docker Desktop, Windows), with the repository at the commit that adds this file:

| Check | Result |
|---|---|
| `docker build --target api` and `--target web` | built; images of 44 MB and 50 MB |
| reproducibility: the `gobuild` stage built twice with `--no-cache` | the three binaries (`api`, `migrate`, `pms-admin`) have identical SHA-256 |
| reproducibility: the `webbuild` stage built twice with `--no-cache` | the 113 files of `dist` have an identical combined hash |
| `docker compose -f deploy/compose.yaml up -d --build` | `db`, `api`, `proxy` healthy; `migrate` applied 62 migrations and exited 0 |
| three migration jobs started together on an empty database | one applied 62, two applied 0, all exit 0, version 62 |
| `/readyz` with the database stopped, then started | 503 then 200 without restart; `/healthz` 200 throughout |
| an API whose database has no migrations | `/healthz` 200, `/readyz` 503 |
| `docker compose stop api` | stopped in 0.9 s, exit 0, log `shutting down` |
| `scripts/prod-smoke.sh` | 42 checks passed (page, fallback, assets, headers, 413, probes, containers non-root and read-only, no published API or database port, client address with forged headers, rate limit per real client) |
| the TLS variant with a self-signed certificate | 308 from http, https 200 with HSTS, TLS 1.2 and 1.3 accepted, 1.1 refused, probes on http |
| headless Chrome on the proxy | the sign-in form is rendered; no Content-Security-Policy violation was logged. Nobody looked at the screens |
| `pg_dump` and `pg_restore` of the database | restored with 62 migrations, the tenant and the user |

Not done here, on purpose: a deployment to a server, a certificate from an authority, load testing, a rehearsed rollback of a release, backup scheduling, monitoring and alerting.

## 13. Moving to a VPS or a cloud instance

Nothing in the stack changes; the checklist is:

1. A machine with Docker, a firewall that opens 80 and 443 (and your admin port) only. Do not open 5432 or 8080: the database and the API are on a private network and must stay there.
2. Copy the repository (or build the two images in CI and push them to a registry, then replace `build:` by `image:` in the compose file).
3. `deploy/.env` with new secrets (`POSTGRES_PASSWORD`, `PMS_JWT_SECRET`), `PMS_BIND=0.0.0.0`, `PMS_PUBLIC_PORT=80`, `PMS_PUBLIC_TLS_PORT=443`.
4. A certificate for your host name in `deploy/certs/` and the TLS file (option A), or a TLS balancer in front (option B).
5. `docker compose ... up -d`, create the first tenant and administrator, run `scripts/prod-smoke.sh` against the public address (`PMS_SMOKE_URL=https://...`, with `SMOKE_NETWORK_TESTS=0` unless you run it on the host).
6. Before the first night audit: accounting set up, backups running, one restore rehearsed (section 9 and audit F-04, F-15).
7. Keep one API instance for the pilot (section 6.3).
