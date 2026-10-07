#!/usr/bin/env bash
# Makes a self-signed certificate for trying the TLS variant of the stack on one machine: deploy/certs/tls.crt and deploy/certs/tls.key. It is NOT for a server that
# other people reach: use a certificate from an authority there (docs/deployment.md, "TLS strategy").
#   deploy/make-dev-cert.sh [host-name]      default: localhost
set -euo pipefail
export MSYS_NO_PATHCONV=1   # Git Bash on Windows would turn the subject "/CN=host" into a path
HOST="${1:-localhost}"
DIR="$(cd "$(dirname "$0")" && pwd)/certs"
mkdir -p "$DIR"
run() {
  if command -v openssl >/dev/null 2>&1; then openssl "$@"
  else docker run --rm -v "$DIR:/certs" -w /certs alpine/openssl "$@"; fi
}
cd "$DIR"
run req -x509 -newkey rsa:2048 -nodes -days 30 -subj "/CN=$HOST" -addext "subjectAltName=DNS:$HOST,IP:127.0.0.1" -keyout tls.key -out tls.crt
# the proxy runs as an unprivileged user and only reads the files
chmod 644 tls.crt tls.key 2>/dev/null || true
echo "wrote $DIR/tls.crt and $DIR/tls.key (valid 30 days, for $HOST)"
