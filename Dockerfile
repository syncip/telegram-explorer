FROM node:26-alpine AS ui
WORKDIR /src/web/ui
COPY web/ui/package.json web/ui/package-lock.json ./
RUN npm ci --no-fund --no-audit
COPY web/ui/ ./
RUN npm run build

FROM golang:1.27-alpine AS app
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web/embed.go ./web/embed.go
COPY --from=ui /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /telex ./cmd/telex

FROM alpine:3.22
RUN apk add --no-cache ca-certificates mailcap \
    && addgroup -S -g 10001 telex && adduser -S -u 10001 -G telex telex \
    && mkdir /data && chown telex:telex /data
COPY --from=app /telex /usr/local/bin/telex
USER telex
ENV DATA_DIR=/data
VOLUME /data
EXPOSE 8080 2222
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/api/state || exit 1
ENTRYPOINT ["/usr/local/bin/telex"]
