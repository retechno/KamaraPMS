#!/usr/bin/env bash
# Smoke test of the production-like stack (deploy/compose.yaml) that is already running:
#
#   docker compose -f deploy/compose.yaml up -d --build
#   scripts/prod-smoke.sh
#
# It checks what a deployment must get right and a green unit test cannot show: the proxy serves the page and the router fallback, the API answers through it, the
# security headers are there, the containers run the way they should, the probes tell alive from ready, and the client address and the rate limit work behind the proxy
# (forged headers from the outside are ignored). It needs Docker (the client address tests run clients inside the compose network, each with an address of its own)
# and curl. Nothing is created in the database; the API is recreated for the rate limit test and is restored at the end.
#
#   PMS_SMOKE_URL           the address of the proxy at the host, default http://127.0.0.1:${PMS_PUBLIC_PORT:-8080}
#   SMOKE_NETWORK_TESTS=0   skip the tests that need Docker network access
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
if [ -f deploy/.env ]; then set -a; . deploy/.env; set +a; fi
BASE="${PMS_SMOKE_URL:-http://127.0.0.1:${PMS_PUBLIC_PORT:-8080}}"
COMPOSE=(docker compose -f deploy/compose.yaml)
NET="kamarapms-deploy_backend"
WEB_IMAGE="kamarapms-web:${PMS_VERSION:-local}"   # busybox wget inside: the client of the network tests
export MSYS_NO_PATHCONV=1

fails=0
ok()  { printf '  PASS  %s\n' "$1"; }
bad() { printf '  FAIL  %s\n' "$1"; fails=$((fails + 1)); }
status() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
body()   { curl -s "$1"; }
# header NAME URL: the value of a response header. awk reads all of the input, so there is no early exit (a pipe closed early is a failure under pipefail).
header() {
  curl -s -D - -o /dev/null "$2" | tr -d '\r' | awk -v h="$1" 'BEGIN { h = tolower(h) ":" } tolower($1) == h && !done { sub(/^[^:]*: */, ""); print; done = 1 }'
}
contains() { case "$1" in *"$2"*) return 0 ;; *) return 1 ;; esac; }
expect() { # expect "name" CONDITION-EXIT-STATUS
  if [ "$2" = 0 ]; then ok "$1"; else bad "$1"; fi
}

echo "== the proxy serves the page and the router fallback"
[ "$(status "$BASE/")" = 200 ]; expect "GET / is the page" $?
contains "$(body "$BASE/")" 'id="app"'; expect "the page has the app root" $?
[ "$(status "$BASE/reservations/12/rooms")" = 200 ]; expect "a deep link is the page (history mode fallback)" $?
ASSET="$(body "$BASE/" | grep -o '/assets/index-[A-Za-z0-9_-]*\.js' | head -1)"
[ -n "$ASSET" ] && [ "$(status "$BASE$ASSET")" = 200 ]; expect "a built asset is served" $?
contains "$(header cache-control "$BASE$ASSET")" immutable; expect "a built asset is cached for a year, immutable" $?
contains "$(header cache-control "$BASE/")" no-cache; expect "the page itself is never cached" $?
[ "$(status "$BASE/assets/nope-123.js")" = 404 ]; expect "a missing asset is 404, not the page" $?

echo "== the API through the proxy, and the headers"
[ "$(status "$BASE/api/v1/properties")" = 401 ]; expect "the API answers through /api (401 without a token)" $?
err="$(body "$BASE/api/v1/properties")"
contains "$err" '"request_id"'; expect "an error carries the request id" $?
if contains "$err" goroutine || contains "$err" '.go:' || contains "$err" panic; then bad "an error does not show internals"; else ok "an error does not show internals"; fi
for h in content-security-policy x-content-type-options x-frame-options referrer-policy permissions-policy; do
  [ -n "$(header $h "$BASE/")" ]; expect "the page has $h" $?
  [ -n "$(header $h "$BASE/api/v1/properties")" ]; expect "the API answer has $h" $?
done
n="$(curl -s -D - -o /dev/null "$BASE/api/v1/properties" | tr -d '\r' | awk 'tolower($1) == "x-frame-options:" { n++ } END { print n + 0 }')"
[ "$n" = 1 ]; expect "a security header is set once, not twice" $?
if [[ "$(header server "$BASE/")" =~ [0-9]\.[0-9] ]]; then bad "the proxy does not tell its version"; else ok "the proxy does not tell its version"; fi
big="$(head -c 3000000 /dev/zero | tr '\0' 'a' | curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/api/v1/auth/login" -H 'Content-Type: application/json' --data-binary @-)"
[ "$big" = 413 ]; expect "a body over the proxy limit is 413 (got $big)" $?

echo "== probes: alive is not ready"
[ "$(status "$BASE/healthz")" = 200 ]; expect "/healthz through the proxy" $?
[ "$(status "$BASE/readyz")" = 200 ]; expect "/readyz through the proxy" $?
contains "$(body "$BASE/readyz")" '"ready"'; expect "/readyz says ready" $?

echo "== the containers"
for svc in db api proxy; do
  state="$("${COMPOSE[@]}" ps --format '{{.Health}}' "$svc" 2>/dev/null | head -1)"
  [ "$state" = healthy ]; expect "$svc is healthy (is '$state')" $?
done
api_id="$("${COMPOSE[@]}" ps -q api)"
db_id="$("${COMPOSE[@]}" ps -q db)"
[ "$(docker inspect --format '{{.Config.User}}' "$api_id")" = "nonroot:nonroot" ]; expect "the API does not run as root" $?
[ "$(docker inspect --format '{{.HostConfig.ReadonlyRootfs}}' "$api_id")" = true ]; expect "the API root file system is read only" $?
[ -z "$(docker port "$api_id")" ]; expect "the API has no published port" $?
[ -z "$(docker port "$db_id")" ]; expect "the database has no published port" $?
[ "$(docker inspect --format '{{.State.ExitCode}}' "$("${COMPOSE[@]}" ps -a -q migrate)")" = 0 ]; expect "the migration job ended with success" $?

if [ "${SMOKE_NETWORK_TESTS:-1}" = 0 ]; then
  echo "(network tests skipped)"
  [ "$fails" = 0 ] && exit 0 || exit 1
fi

echo "== the client address behind the proxy"
A=172.29.0.51
B=172.29.0.52
# a wget inside the compose network with an address of its own
client() { local ip="$1"; shift; docker run --rm --network "$NET" --ip "$ip" --entrypoint wget "$WEB_IMAGE" "$@" 2>&1; }
# the client_ip that the API logged for the request to a unique path
logged_ip() { "${COMPOSE[@]}" logs --no-log-prefix api 2>/dev/null | grep "\"path\":\"$1\"" | head -1 | sed -n 's/.*"client_ip":"\([^"]*\)".*/\1/p'; }

p="/api/v1/smoke-proxy-$$"
client $A -q -O /dev/null --header "X-Forwarded-For: 9.9.9.9" "http://proxy:8080$p" >/dev/null
[ "$(logged_ip "$p")" = "$A" ]; expect "through the proxy: a forged X-Forwarded-For is replaced, the client is $A (logged '$(logged_ip "$p")')" $?

p="/api/v1/smoke-direct-$$"
client $A -q -O /dev/null --header "X-Forwarded-For: 9.9.9.9" "http://api:8080$p" >/dev/null
[ "$(logged_ip "$p")" = "$A" ]; expect "straight to the API: a forged X-Forwarded-For from an untrusted peer is ignored (logged '$(logged_ip "$p")')" $?

p="/api/v1/smoke-claim-$$"
client $A -q -O /dev/null --header "X-Forwarded-For: 172.29.0.10" "http://api:8080$p" >/dev/null
[ "$(logged_ip "$p")" = "$A" ]; expect "straight to the API: claiming to be the proxy does not make a peer trusted (logged '$(logged_ip "$p")')" $?

echo "== the rate limit counts the real client (6 a minute for this test)"
PMS_RATE_LIMIT_PER_MINUTE=6 "${COMPOSE[@]}" up -d --no-deps --force-recreate api >/dev/null 2>&1
for _ in $(seq 1 40); do [ "$(status "$BASE/readyz")" = 200 ] && break; sleep 1; done
# the status codes of a series of requests from one client, in one container so that nothing refills in between
codes() { # codes <ip> <count> <url> [header]
  local hdr=""
  [ -n "${4:-}" ] && hdr="--header '$4'"
  docker run --rm --network "$NET" --ip "$1" --entrypoint sh "$WEB_IMAGE" -c "for i in \$(seq 1 $2); do wget -S -q -O /dev/null $hdr '$3' 2>&1 | grep -m1 'HTTP/' | awk '{print \$2}'; done"
}
a_codes="$(codes $A 12 http://proxy:8080/api/v1/properties)"
a_ok=$(printf '%s\n' "$a_codes" | grep -c '^401$' || true)
a_429=$(printf '%s\n' "$a_codes" | grep -c '^429$' || true)
[ "$a_ok" -ge 5 ] && [ "$a_429" -ge 5 ]; expect "client $A has its own limit ($a_ok allowed, then $a_429 refused of 12)" $?
b_codes="$(codes $B 3 http://proxy:8080/api/v1/properties)"
[ "$(printf '%s\n' "$b_codes" | grep -c '^401$' || true)" = 3 ]; expect "client $B, behind the same proxy, is not limited by the traffic of $A (got $(printf '%s' "$b_codes" | tr '\n' ' '))" $?
# the same client, at once, forging the header: still the same bucket
f_codes="$(docker run --rm --network "$NET" --ip $A --entrypoint sh "$WEB_IMAGE" -c "
  for i in 1 2 3 4 5 6 7 8; do wget -S -q -O /dev/null http://proxy:8080/api/v1/properties 2>&1 | grep -m1 'HTTP/' | awk '{print \$2}'; done
  echo ---
  for ip in 8.8.8.8 8.8.4.4 1.1.1.1; do wget -S -q -O /dev/null --header \"X-Forwarded-For: \$ip\" http://proxy:8080/api/v1/properties 2>&1 | grep -m1 'HTTP/' | awk '{print \$2}'; done
  echo ---
  for ip in 8.8.8.8 8.8.4.4 1.1.1.1; do wget -S -q -O /dev/null --header \"X-Forwarded-For: \$ip\" http://api:8080/api/v1/properties 2>&1 | grep -m1 'HTTP/' | awk '{print \$2}'; done")"
via_proxy="$(printf '%s\n' "$f_codes" | sed -n '/---/{n;:a;/---/q;p;n;ba}')"
direct="$(printf '%s\n' "$f_codes" | awk '/---/ { n++ } n == 2 && !/---/')"
[ "$(printf '%s\n' "$via_proxy" | grep -c '^429$' || true)" = 3 ]; expect "forging X-Forwarded-For through the proxy gives no fresh bucket (got $(printf '%s' "$via_proxy" | tr '\n' ' '))" $?
[ "$(printf '%s\n' "$direct" | grep -c '^429$' || true)" = 3 ]; expect "forging the header straight to the API gives no fresh bucket (got $(printf '%s' "$direct" | tr '\n' ' '))" $?
"${COMPOSE[@]}" up -d --no-deps --force-recreate api >/dev/null 2>&1
for _ in $(seq 1 40); do [ "$(status "$BASE/readyz")" = 200 ] && break; sleep 1; done
[ "$(status "$BASE/readyz")" = 200 ]; expect "the API is restored with its normal limit" $?

echo
if [ "$fails" = 0 ]; then echo "SMOKE TEST PASSED"; else echo "SMOKE TEST FAILED: $fails check(s)"; exit 1; fi
