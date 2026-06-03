# syntax=docker/dockerfile:1

# --- build stage -------------------------------------------------------------
FROM golang:1.22-alpine AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src

# Copy sources and resolve deps. go.sum is generated here (the dev machine has
# no Go toolchain), so `go mod tidy` runs before the build.
COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /tempmon ./cmd/tempmon

# --- runtime stage (static, minimal) ----------------------------------------
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /tempmon /tempmon

ENV HTTP_ADDR=":8080" \
    DB_PATH="/data/tempmon.db"
EXPOSE 8080
ENTRYPOINT ["/tempmon"]
