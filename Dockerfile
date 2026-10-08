# syntax=docker/dockerfile:1
#
# Production image for the B-Edge API.
#
#   docker build -t b-edge-api .
#   docker run --env-file prod.env -p 3000:3000 b-edge-api                  # the API
#   docker run --env-file prod.env --entrypoint /app/migrate b-edge-api     # migrations
#
# Configuration comes only from the environment: main.go's godotenv.Load()
# finds no .env in the image (.dockerignore allows none in) and says so.
# config.requiredEnvVars refuses to boot without the database, JWT, CORS,
# APP_ENV and Cloudinary settings.

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# docs/ is generated and gitignored, but main.go imports it to serve
# /swagger. Generated here with the version go.mod pins, so the image never
# depends on whatever swag happens to be installed on a laptop.
RUN go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g cmd/main.go -o docs

# Deliberately NO -tags devbypass. That tag compiles in the fixed OTP code
# 000000 (internal/pkg/devbypass); without it the bypass does not exist in
# the binary at all, whatever APP_ENV says. Same rule as `make build`.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/b-edge ./cmd/main.go \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

# distroless/static: CA certificates for Twilio and Cloudinary, no shell, no
# package manager. Time zones are embedded in the binary (time/tzdata in
# main.go), so Asia/Beirut resolves without a tzdata package.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/b-edge /out/migrate /app/
# cmd/migrate reads file://db/migrations relative to the working directory.
# --chmod because the image runs as nonroot and COPY keeps the files' modes:
# eight migrations on the founder's disk are 0600 (git does not record that),
# and the first build of this image stopped at version 4 on "permission
# denied" reading 005.
COPY --chmod=0755 db/migrations /app/db/migrations
ENV PORT=3000
EXPOSE 3000
USER nonroot:nonroot
ENTRYPOINT ["/app/b-edge"]
