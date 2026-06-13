ARG GO_VERSION=1.26

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-s -w" \
    -o /out/turn-proxy ./cmd/turn-proxy

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/turn-proxy/turn-proxy-go"
LABEL org.opencontainers.image.description="UDP tunnel disguised as WebRTC media over TURN"
LABEL org.opencontainers.image.licenses="MIT"
COPY --from=build /out/turn-proxy /turn-proxy
ENTRYPOINT ["/turn-proxy"]
CMD ["-config", "/etc/turn-proxy/turn-proxy.json"]
