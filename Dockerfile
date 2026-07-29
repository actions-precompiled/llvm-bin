# LLVM build environment only (cmake, ninja, compilers).
# The package Go binary is bind-mounted as /apc and run as: /apc work
FROM ubuntu:24.04

ARG TARGETARCH=
ARG CMAKE_VERSION=3.31.6

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        ninja-build \
        git \
        curl \
        python3 \
        pkg-config \
        patchelf \
        file \
        tar \
        xz-utils \
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

RUN set -eux; \
    arch="$(uname -m)"; \
    case "$arch" in \
      x86_64)  cmake_arch=x86_64 ;; \
      aarch64) cmake_arch=aarch64 ;; \
      *) echo "unsupported arch $arch" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://github.com/Kitware/CMake/releases/download/v${CMAKE_VERSION}/cmake-${CMAKE_VERSION}-linux-${cmake_arch}.tar.gz" \
      | tar -xz -C /usr/local --strip-components=1; \
    cmake --version

WORKDIR /work
# No ENTRYPOINT — host mounts /apc and runs: /apc work
