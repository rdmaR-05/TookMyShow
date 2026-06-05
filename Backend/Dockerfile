
FROM golang:1.25.5-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o main .

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/main .

RUN addgroup -S nonroot \
    && adduser -S nonroot -G nonroot \
    && chown -R nonroot:nonroot /app

USER nonroot

EXPOSE 8000

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8000/ || exit 1

CMD ["./main"]