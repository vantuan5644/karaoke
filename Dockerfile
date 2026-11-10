# syntax=docker/dockerfile:1
# Karaoke with everything it can use: yt-dlp for videos that refuse to embed, deno for the
# JavaScript challenge YouTube now sets yt-dlp, and ffmpeg to join video and audio.

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/karaoke .

FROM debian:trixie-slim
ARG TARGETARCH
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl ffmpeg unzip \
 && rm -rf /var/lib/apt/lists/*
# yt-dlp's standalone build bundles its own Python and its YouTube challenge solver scripts.
RUN set -eux; \
    case "$TARGETARCH" in \
      amd64) ytdlp=yt-dlp_linux; deno=deno-x86_64-unknown-linux-gnu.zip ;; \
      arm64) ytdlp=yt-dlp_linux_aarch64; deno=deno-aarch64-unknown-linux-gnu.zip ;; \
      *) echo "unsupported arch $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /usr/local/bin/yt-dlp "https://github.com/yt-dlp/yt-dlp/releases/latest/download/$ytdlp"; \
    chmod 0755 /usr/local/bin/yt-dlp; \
    curl -fsSL -o /tmp/deno.zip "https://github.com/denoland/deno/releases/latest/download/$deno"; \
    unzip -q /tmp/deno.zip -d /usr/local/bin; rm /tmp/deno.zip; \
    yt-dlp --version; deno --version
COPY --from=build /out/karaoke /usr/local/bin/karaoke
RUN useradd --system --create-home --home-dir /data karaoke
USER karaoke
VOLUME ["/data"]
EXPOSE 8765
HEALTHCHECK --interval=30s --timeout=3s CMD curl -fsS http://127.0.0.1:8765/healthz || exit 1
ENTRYPOINT ["karaoke", "-listen", ":8765", "-data", "/data/state", "-cache", "/data/cache"]
