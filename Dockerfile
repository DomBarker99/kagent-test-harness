# kagent-test-harness: a Go shell (kagent's BYO contract) plus a TypeScript loop.
#
# One file, two images with the same shell and loop on different bases:
#   alpine  (the default: the last stage)  node:26-alpine, small, busybox tools
#   debian  (--target debian)              node:26-trixie, full Debian 13 plus debugging tools
# GitHub Actions builds both on every push to main (.github/workflows/image.yaml).
#
# Build one by hand for the cluster (amd64) and push; the Harness needs the pushed digest:
#   docker buildx build --platform linux/amd64 --provenance=false [--target debian] \
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

# Stage 2, Debian: the full node image (Debian 13 with compilers, git, curl, python3)
# plus the tools used to look around a sandbox. This is a test image, not a lean one.
FROM node:26-trixie AS debian
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      bind9-dnsutils iproute2 iputils-ping net-tools netcat-openbsd nmap socat tcpdump traceroute \
      htop lsof ltrace psmisc strace \
      file jq less nano ripgrep rsync sqlite3 tmux tree unzip vim zip \
      python3-pip python3-venv && \
    rm -rf /var/lib/apt/lists/*
# Same runtime user and directories as the Alpine image below.
RUN groupadd -g 65532 kagent && \
    useradd -u 65532 -g kagent -m -d /home/kagent -s /bin/bash kagent && \
    mkdir -p /config /data && \
    chown -R 65532:65532 /config /data
COPY --from=shell /harness-shell /usr/local/bin/harness-shell
COPY loop/loop.ts /app/loop.ts
USER 65532:65532
ENV HOME=/home/kagent
ENTRYPOINT ["/usr/local/bin/harness-shell"]

# Stage 2, Alpine: Node runs loop.ts directly (type stripping), so there is no TypeScript build step.
FROM node:26-alpine AS alpine
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
