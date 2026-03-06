FROM golang:1.24-bookworm

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        build-essential git ca-certificates curl && \
    rm -rf /var/lib/apt/lists/*

# Install Node.js 22 (needed for tree-sitter generate).
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    apt-get install -y --no-install-recommends nodejs && \
    rm -rf /var/lib/apt/lists/*

# Install tree-sitter-cli for grammar regeneration.
RUN npm install -g tree-sitter-cli@0.24.7

ENV CGO_ENABLED=1

WORKDIR /src
