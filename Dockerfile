FROM golang:1.25-alpine AS builder

WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/hisabji-api ./cmd/api

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata && adduser -D -H -u 10001 appuser
WORKDIR /app
COPY --from=builder /out/hisabji-api /app/hisabji-api
USER appuser
EXPOSE 8080
ENTRYPOINT ["/app/hisabji-api"]

FROM golang:1.25-alpine AS development

RUN apk add --no-cache git ca-certificates
RUN go install github.com/air-verse/air@v1.61.7
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
EXPOSE 8080
CMD ["air", "-c", ".air.toml"]