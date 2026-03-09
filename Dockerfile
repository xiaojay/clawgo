FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /clawgo ./cmd/clawgo

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /clawgo /usr/local/bin/clawgo
EXPOSE 8402
ENTRYPOINT ["clawgo"]
