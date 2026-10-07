# syntax=docker/dockerfile:1
#
# KamaraPMS images. Two targets are built from this one file:
#
#   api  the Go API, the migration job and the admin tool, on a minimal runtime image (no shell, no package manager, not root)
#   web  the reverse proxy: nginx serving the built single-page app and forwarding /api to the API
#
#   docker build --target api -t kamarapms-api .
#   docker build --target web -t kamarapms-web .
#
# Nothing secret is in an image: every setting comes from the environment when the container starts (deploy/.env.example, docs/deployment.md).
# The images are built from the source of the repository and from the base images named below; to pin a base image exactly, pass its digest, for example
#   --build-arg GO_IMAGE=golang:1.26-trixie@sha256:<digest>

ARG GO_IMAGE=golang:1.26-trixie
ARG NODE_IMAGE=node:22-alpine
ARG NGINX_IMAGE=nginxinc/nginx-unprivileged:1.27-alpine
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot

# ---------------------------------------------------------------------------------------------------------------------------------------------------------------
FROM ${GO_IMAGE} AS gobuild
WORKDIR /src
# A static binary (the drivers are pure Go and the time zone data is embedded), no toolchain download, no change to go.mod or go.sum during the build.
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
# -trimpath and -buildvcs=false keep the build free of paths and of the state of a git checkout, so the same source gives the same binary.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/ ./cmd/api ./cmd/migrate ./cmd/pms-admin

# ---------------------------------------------------------------------------------------------------------------------------------------------------------------
FROM ${RUNTIME_IMAGE} AS api
COPY --from=gobuild /out/api /out/migrate /out/pms-admin /usr/local/bin/
USER nonroot:nonroot
ENV PMS_ENV=production PMS_HTTP_ADDR=:8080
EXPOSE 8080
# Ready, not only alive: the container is healthy when the database answers and the schema has the migrations of this release. The image has no shell or curl, so the
# check is the binary itself.
HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=3 CMD ["/usr/local/bin/api", "healthcheck", "-ready"]
ENTRYPOINT ["/usr/local/bin/api"]

# ---------------------------------------------------------------------------------------------------------------------------------------------------------------
FROM ${NODE_IMAGE} AS webbuild
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
RUN npm run build

# ---------------------------------------------------------------------------------------------------------------------------------------------------------------
FROM ${NGINX_IMAGE} AS web
COPY deploy/nginx/snippets/ /etc/nginx/snippets/
COPY deploy/nginx/default.conf.template /etc/nginx/templates/default.conf.template
COPY --from=webbuild /web/dist /usr/share/nginx/html
# The address of the API as the proxy reaches it (the name of the service in the compose file). Only API_* variables are substituted into the templates, so the
# nginx variables ($host, $remote_addr, ...) are left alone.
ENV API_UPSTREAM=api:8080 NGINX_ENVSUBST_FILTER=^API_
EXPOSE 8080
