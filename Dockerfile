# ---- Build stage ---------------------------------------------------
FROM golang:1.23-alpine AS build

WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /meatloaf-server

# ---- Minimal runtime image -----------------------------------------
FROM scratch

WORKDIR /data

# Copy binary only
COPY --from=build /meatloaf-server /meatloaf-server

EXPOSE 80

# Serve /data on port 80
ENTRYPOINT ["/meatloaf-server", "-addr", ":80", "-root", "/data"]

