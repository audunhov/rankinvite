# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the application
RUN go build -o /app/rankinvite cmd/rankinvite/main.go

# Final stage
FROM alpine:latest

WORKDIR /app

# Copy the binary from the build stage
COPY --from=builder /app/rankinvite /app/rankinvite

# Expose port 8080
EXPOSE 8080

# Run the application
ENTRYPOINT ["/app/rankinvite"]
