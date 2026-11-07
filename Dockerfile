FROM golang:1.27.3-alpine3.24@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS builder
# CGO for go-sqlite3
RUN apk add --no-cache build-base
WORKDIR /api
COPY ./server/go.mod ./server/go.sum ./
RUN go mod download
COPY ./server .
RUN CGO_ENABLED=1 go test ./... && CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o ./app ./main.go

FROM node:24.22.2-alpine3.24@sha256:a34ab6d7f6f4813f4d0c5469d74685604b962fbdbf5793a7bc7b4998ecb867c8 AS build-stage
WORKDIR /app
COPY ./ui/package.json ./ui/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY ./ui .
RUN npm run build

FROM alpine:3.24.4@sha256:93b41ef40b1677fce8fcc64ff4af09daddfacf5040afde618bc46c1e2d1cc7d4
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
