FROM golang:1.24-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    ca-certificates \
    curl \
    git \
    gnupg \
    pkg-config \
    time \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
RUN apt-get update && apt-get install -y --no-install-recommends \
    nodejs \
    && rm -rf /var/lib/apt/lists/*

RUN npm --global --loglevel=warn install tree-sitter-cli@0.24.7

WORKDIR /workspace

ENV PATH="/usr/local/go/bin:${PATH}"
ENV CGO_ENABLED=1
ENV GO111MODULE=on
