# syntax=docker/dockerfile:1

# --- build stage -------------------------------------------------------------
FROM golang:1.22-alpine AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src

# Resolve deps first (cached layer; only re-runs when go.mod/go.sum change).
# go.sum is committed, so the build is reproducible and checksum-verified.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /tempmon ./cmd/tempmon

# --- runtime stage (static, minimal) ----------------------------------------
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /tempmon /tempmon

ENV HTTP_ADDR=":8080" \
    DB_PATH="/data/tempmon.db"
EXPOSE 8080
ENTRYPOINT ["/tempmon"]
