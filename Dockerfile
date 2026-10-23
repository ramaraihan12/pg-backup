# syntax=docker/dockerfile:1
ARG GO_VERSION=1.27.3
ARG ALPINE_VERSION=3.24

# dev: Go toolchain plus the Postgres 17 client tools. Used for tests and builds.
FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS dev
RUN apk add --no-cache postgresql17-client build-base git
WORKDIR /src

FROM dev AS build
ARG VERSION=dev
# Dependencies first so code changes don't re-download them.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/ ./cmd/pg-backup ./cmd/demodata

FROM alpine:${ALPINE_VERSION} AS runtime
RUN apk add --no-cache postgresql17-client ca-certificates tzdata \
    && adduser -D -u 10001 app
COPY --from=build /out/pg-backup /out/demodata /usr/local/bin/
USER app
EXPOSE 9187
ENTRYPOINT ["pg-backup"]
CMD ["serve"]
