# Native multi-arch LLVM build image (Ubuntu 24.04).
# Full kitchen-sink compile lives in scripts/build_and_package.sh — this image only holds deps.
FROM ubuntu:24.04

ARG TARGETARCH=

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        cmake \
        ninja-build \
        git \
        curl \
        python3 \
        python3-pip \
        pkg-config \
        patchelf \
        file \
        tar \
        xz-utils \
        zip \
        unzip \
        zlib1g-dev \
        libzstd-dev \
        libxml2-dev \
        libncurses-dev \
        libedit-dev \
        libcurl4-openssl-dev \
        swig \
        liblzma-dev \
        binutils-dev \
        ccache \
    && rm -rf /var/lib/apt/lists/*

# Disk is the real constraint on GHA; prefer /tmp for huge trees when mounted.
WORKDIR /src
COPY scripts/build_and_package.sh /usr/local/bin/build_and_package.sh
RUN chmod +x /usr/local/bin/build_and_package.sh

ENTRYPOINT ["/usr/local/bin/build_and_package.sh"]
