FROM golang:1.26-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/xgateway ./cmd/xgateway

FROM alpine:3.21
WORKDIR /app

RUN addgroup -S app && adduser -S app -G app
COPY --from=builder /out/xgateway /usr/local/bin/xgateway

EXPOSE 8081

USER app
ENTRYPOINT ["xgateway"]
