# syntax=docker/dockerfile:1.7
ARG GO_IMAGE=golang:1.26.1-bookworm
FROM ${GO_IMAGE} AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -buildvcs=true -ldflags="-s -w -buildid=" -o /out/charm-registry ./cmd/charm-registry

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/charm-registry /usr/local/bin/charm-registry

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/charm-registry"]
