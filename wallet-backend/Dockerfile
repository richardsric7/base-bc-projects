FROM golang:1.26-alpine AS builder

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

RUN apk add --no-cache ca-certificates

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /dist/wallet-backend .

FROM alpine:latest
RUN apk add --no-cache ca-certificates

COPY --from=builder /dist/wallet-backend /wallet-backend

ENTRYPOINT ["/wallet-backend"]
