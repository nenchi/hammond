FROM golang:1.27.4-alpine3.24@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS builder
# CGO for go-sqlite3
RUN apk add --no-cache build-base
WORKDIR /api
COPY ./server/go.mod ./server/go.sum ./
RUN go mod download
COPY ./server .
RUN CGO_ENABLED=1 go test ./... && CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o ./app ./main.go

FROM node:26.13.1-alpine3.24@sha256:6bd9ab43c0ec0190ca7b2d193906cb8b7edf6e7f4afa43ddeccd6452710bffb5 AS build-stage
WORKDIR /app
COPY ./ui/package.json ./ui/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY ./ui .
COPY ./VERSION /VERSION
RUN VUE_APP_VERSION="$(cat /VERSION)" npm run build

FROM alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
LABEL org.opencontainers.image.source="https://github.com/nenchi/hammond"
ENV CONFIG=/config \
    DATA=/assets \
    GIN_MODE=release
RUN apk upgrade --no-cache && apk add --no-cache ca-certificates tzdata \
 && mkdir -p /config /assets /api && chmod 777 /config /assets
VOLUME ["/config", "/assets"]
WORKDIR /api
COPY --from=builder /api/app .
COPY --from=build-stage /app/dist ./dist
EXPOSE 3000
ENTRYPOINT ["./app"]
