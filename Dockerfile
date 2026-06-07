# Build stage
FROM golang:1.25.9-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the application (Pure Go, no CGO needed for modernc.org/sqlite)
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /app/rankinvite cmd/rankinvite/main.go

# Final stage
FROM alpine:latest

# Install CA certificates for SMTP/HTTPS
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Create data directory for SQLite database
RUN mkdir -p /app/data

# Copy the binary from the build stage
COPY --from=builder /app/rankinvite /app/rankinvite

# Environment defaults
ENV PORT=8080
ENV DATABASE_URL=/app/data/rankinvite.db

# Expose port
EXPOSE 8080

# Healthcheck
HEALTHCHECK --interval=30s --timeout=3s \
  CMD wget -qO- http://localhost:${PORT}/login || exit 1

# Run the application
ENTRYPOINT ["/app/rankinvite"]
