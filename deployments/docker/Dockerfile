# syntax=docker/dockerfile:1
# One Dockerfile for every Go service. Pick the service with --build-arg SERVICE=<name>.

# ---------- build stage: full Go toolchain ----------
FROM golang:1.25-alpine AS build
ARG SERVICE
WORKDIR /src

# Copy module files first so `go mod download` stays cached until deps change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY pkg ./pkg
COPY services/${SERVICE} ./services/${SERVICE}

# CGO off -> static binary that runs on a minimal base image.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./services/${SERVICE}/cmd

# Writable data dir. A new named volume copies this dir's ownership, so the
# non-root user can write resumes to it.
RUN mkdir -p /out/data/resumes

# ---------- runtime stage: tiny, no shell, non-root ----------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
COPY --from=build --chown=nonroot:nonroot /out/data /data
USER nonroot:nonroot
ENTRYPOINT ["/app"]
