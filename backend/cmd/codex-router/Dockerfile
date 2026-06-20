FROM golang:1.26.4-alpine AS builder

WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/codex-router ./cmd/codex-router

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/codex-router /usr/local/bin/codex-router
EXPOSE 8089
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/codex-router"]
