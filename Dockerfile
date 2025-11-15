FROM golang:1.24 AS builder
WORKDIR /src

# Download deps first (cache layer)
COPY go.mod go.sum ./
RUN go mod download

# Copy sources and build
COPY . .
ENV CGO_ENABLED=0
RUN GOOS=linux go build -ldflags="-s -w" -o /slmcache ./cmd/slmcache

FROM alpine:3.18
RUN apk add --no-cache ca-certificates
COPY --from=builder /slmcache /usr/local/bin/slmcache
WORKDIR /data
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/slmcache"]
