# ---- 1: build SPA ----
# $BUILDPLATFORM keeps node on the builder's native arch — the SPA output is
# arch-independent, so the arm64 half of a multi-arch build never emulates node.
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

# ---- 2: build binary (embed SPA) ----
# Must be >= the `go` directive in go.mod (1.26), else the build breaks or
# GOTOOLCHAIN silently downloads a second toolchain inside the image build.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
# TARGETARCH is set by buildx per target platform; with CGO_ENABLED=0 the Go
# build is a pure cross-compile, so arm64 no longer runs under QEMU.
ARG TARGETARCH
ENV GOARCH=${TARGETARCH}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN rm -rf internal/api/webroot
COPY --from=web /src/web/dist internal/api/webroot
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/wiminder ./cmd/server

# ---- 3: image final ----
# Runs as root at boot only for the entrypoint's data-dir chown; the server
# process itself runs as UID 1000 (see deploy/docker-entrypoint.sh).
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata wget su-exec \
    && addgroup -g 1000 app && adduser -D -u 1000 -G app app
COPY --from=build /out/wiminder /usr/local/bin/wiminder
COPY --chmod=0755 deploy/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
ENV ADDR=:8080 DATA_DIR=/data
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
  CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["wiminder"]
