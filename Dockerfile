# kagent-test-harness: a Go shell (kagent's BYO contract) plus a TypeScript loop.
#
# Build for the cluster (amd64) and push; the Harness needs the pushed digest:
#   docker buildx build --platform linux/amd64 --provenance=false \
#     -t <registry>/kagent-test-harness:dev --push .
#   docker buildx imagetools inspect <registry>/kagent-test-harness:dev   # prints the sha256 digest

# Stage 1: compile the shell. Runs on the build machine, cross-compiles for the target.
FROM --platform=$BUILDPLATFORM golang:1.27 AS shell
ARG TARGETARCH
WORKDIR /src
COPY shell/ ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath -o /harness-shell .

# Stage 2: Node runs loop.ts directly (type stripping), so there is no TypeScript build step.
FROM node:26-alpine
# Same runtime user as kagent's own images. The shell writes the compiled config
# to /config and the bootstrap bundle under /data (the Actor's durable volume).
RUN addgroup -S -g 65532 kagent && \
    adduser -S -D -u 65532 -G kagent -h /home/kagent kagent && \
    mkdir -p /config /data && \
    chown -R 65532:65532 /config /data
COPY --from=shell /harness-shell /usr/local/bin/harness-shell
COPY loop/loop.ts /app/loop.ts
USER 65532:65532
ENV HOME=/home/kagent
ENTRYPOINT ["/usr/local/bin/harness-shell"]
