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
# A named volume copies this directory's ownership on first use, so the nonroot user can write to it.
COPY --from=build --chown=65532:65532 /out/data /data
ENV AGB_ADDR=0.0.0.0:8420 \
    AGB_DATA_DIR=/data
EXPOSE 8420
VOLUME ["/data"]
USER nonroot
ENTRYPOINT ["/agb", "serve"]
