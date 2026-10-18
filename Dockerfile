FROM node:22.22.2-bookworm-slim AS web
# web/package.json names no package manager, so the pnpm version is fixed here.
RUN corepack enable && corepack prepare pnpm@11.20.0 --activate
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml web/.npmrc ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/agb ./cmd/agb \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/agb /agb
COPY --from=web /src/web/dist /web
# A named volume copies this directory's ownership on first use, so the nonroot user can write to it.
COPY --from=build --chown=65532:65532 /out/data /data
ENV AGB_ADDR=0.0.0.0:8420 \
    AGB_DATA_DIR=/data \
    AGB_WEB_DIR=/web
EXPOSE 8420
VOLUME ["/data"]
USER nonroot
ENTRYPOINT ["/agb", "serve"]
