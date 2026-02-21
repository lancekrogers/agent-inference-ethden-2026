FROM golang:1.24-alpine AS builder
RUN apk add --no-cache git ca-certificates build-base linux-headers
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o /src/bin/agent-inference ./cmd/agent-inference

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /src/bin/agent-inference /usr/local/bin/agent-inference
ENTRYPOINT ["agent-inference"]
