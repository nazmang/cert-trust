# ---------- Build stage ----------
FROM public.ecr.aws/docker/library/golang:1.22 AS builder
WORKDIR /workspace

# Enable Go modules and caching
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy source
COPY . .

# Build
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /manager ./cmd/cert-trust

# ---------- Runtime stage ----------
FROM gcr.io/distroless/static:nonroot
LABEL org.opencontainers.image.source https://github.com/nazmang/cert-trust
USER nonroot:nonroot
WORKDIR /
COPY --from=builder /manager /manager

ENTRYPOINT ["/manager"]
