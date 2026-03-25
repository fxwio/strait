FROM golang:1.24-alpine AS builder
WORKDIR /app
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o gateway ./cmd/gateway/main.go

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=builder /app/gateway /gateway
EXPOSE 8080
ENTRYPOINT ["/gateway"]
