# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /glitchgate .

FROM alpine:3.21
RUN addgroup -S glitchgate && adduser -S -G glitchgate glitchgate

COPY --from=builder /glitchgate /usr/local/bin/glitchgate

# Default data directory — mount a volume here for persistent DB and config
WORKDIR /data
RUN chown glitchgate:glitchgate /data

USER glitchgate
EXPOSE 4000
VOLUME ["/data"]

ENTRYPOINT ["glitchgate"]
CMD ["serve", "--config", "/data/config.yaml"]
