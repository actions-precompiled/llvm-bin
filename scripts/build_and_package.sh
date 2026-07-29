#!/usr/bin/env bash
# Build a relocatable LLVM/Clang kitchen-sink distribution from llvm-project.
# VERSION: tag (llvmorg-21.1.0), "trunk"/"main", or "trunk-<sha>".
set -euo pipefail

VERSION_RAW="${LLVM_VERSION:-${PACKAGE_VERSION:?set LLVM_VERSION or PACKAGE_VERSION}}"
BUILD_TARGET="${BUILD_TARGET:-linux-amd64}"
OUTPUT_DIR="${OUTPUT_DIR:-/out}"
UPSTREAM_REPO="${UPSTREAM_REPO:-https://github.com/llvm/llvm-project.git}"
PACKAGE_NAME="${PACKAGE_NAME:-llvm}"
JOBS="${JOBS:-$(nproc)}"
DISTRIBUTOR="${DISTRIBUTOR:-actions-precompiled}"
# Parallel link jobs — keep low to avoid OOM on GHA.
LLVM_PARALLEL_LINK_JOBS="${LLVM_PARALLEL_LINK_JOBS:-2}"
# Optional: limit targets (default host arch triple family)
LLVM_TARGETS_TO_BUILD="${LLVM_TARGETS_TO_BUILD:-Native}"
# Projects: full utility stack (not every experimental backend).
LLVM_ENABLE_PROJECTS="${LLVM_ENABLE_PROJECTS:-clang;clang-tools-extra;lld;lldb;mlir;polly;bolt;openmp}"
LLVM_ENABLE_RUNTIMES="${LLVM_ENABLE_RUNTIMES:-compiler-rt;libcxx;libcxxabi;libunwind}"

case "$BUILD_TARGET" in
  linux-amd64|linux-x86_64) ARCHIVE_SUFFIX="linux-amd64" ;;
  linux-aarch64|linux-arm64) ARCHIVE_SUFFIX="linux-aarch64" ;;
  *)
    echo "Unsupported BUILD_TARGET for Linux script: $BUILD_TARGET" >&2
    exit 1
    ;;
esac

WORKDIR="${WORKDIR:-/tmp/${PACKAGE_NAME}-build}"
SRC_DIR="${WORKDIR}/src"
BUILD_DIR="${WORKDIR}/build"
STAGE_DIR="${WORKDIR}/stage"
PREFIX_DIR="${STAGE_DIR}/${PACKAGE_NAME}"

rm -rf "$WORKDIR"
mkdir -p "$SRC_DIR" "$BUILD_DIR" "$PREFIX_DIR" "$OUTPUT_DIR"

echo "========================================="
echo "Building ${PACKAGE_NAME} ${VERSION_RAW}"
echo "  BUILD_TARGET: ${BUILD_TARGET}"
echo "  PROJECTS:     ${LLVM_ENABLE_PROJECTS}"
echo "  RUNTIMES:     ${LLVM_ENABLE_RUNTIMES}"
echo "  TARGETS:      ${LLVM_TARGETS_TO_BUILD}"
echo "========================================="

# --- resolve ref ---
REF=""
ARTIFACT_VERSION=""
GIT_SHA=""

clone_upstream() {
  local ref="$1"
  # shallow for tags; trunk needs enough history for version scrapes sometimes — depth 1 is fine
  git clone --depth 1 --branch "$ref" "$UPSTREAM_REPO" "$SRC_DIR"
}

if [[ "$VERSION_RAW" == "trunk" || "$VERSION_RAW" == "main" || "$VERSION_RAW" == "git-main" ]]; then
  REF="main"
  if ! clone_upstream main; then
    rm -rf "$SRC_DIR"
    mkdir -p "$SRC_DIR"
    clone_upstream master
    REF="master"
  fi
  GIT_SHA="$(git -C "$SRC_DIR" rev-parse --short=12 HEAD)"
  ARTIFACT_VERSION="trunk-${GIT_SHA}"
elif [[ "$VERSION_RAW" == trunk-* ]]; then
  # trunk-<sha> or trunk-yyyymmdd — try as branch tip first, else treat suffix as sha
  REF="main"
  clone_upstream main || { rm -rf "$SRC_DIR"; mkdir -p "$SRC_DIR"; clone_upstream master; REF=master; }
  GIT_SHA="$(git -C "$SRC_DIR" rev-parse --short=12 HEAD)"
  ARTIFACT_VERSION="trunk-${GIT_SHA}"
else
  # llvmorg-X.Y.Z style tags (sometimes with/without prefix)
  REF="$VERSION_RAW"
  if ! clone_upstream "$REF" 2>/dev/null; then
    rm -rf "$SRC_DIR"
    mkdir -p "$SRC_DIR"
    if [[ "$VERSION_RAW" != llvmorg-* ]]; then
      clone_upstream "llvmorg-${VERSION_RAW#v}"
      REF="llvmorg-${VERSION_RAW#v}"
    else
      echo "Failed to clone ref ${VERSION_RAW}" >&2
      exit 1
    fi
  fi
  GIT_SHA="$(git -C "$SRC_DIR" rev-parse --short=12 HEAD)"
  ARTIFACT_VERSION="${VERSION_RAW#v}"
  ARTIFACT_VERSION="${ARTIFACT_VERSION#llvmorg-}"
  # keep full tag-ish name for artifacts when it's a release
  if [[ "$VERSION_RAW" == llvmorg-* ]]; then
    ARTIFACT_VERSION="${VERSION_RAW}"
  fi
fi

echo "Resolved ref=${REF} sha=${GIT_SHA} artifact_version=${ARTIFACT_VERSION}"

# --- configure ---
# Kitchen sink tools; Native backend keeps disk/time within GHA free-runner ballpark.
# Override LLVM_TARGETS_TO_BUILD for multi-target cross compilers.
cmake -G Ninja -S "${SRC_DIR}/llvm" -B "${BUILD_DIR}" \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_INSTALL_PREFIX="${PREFIX_DIR}" \
  -DCMAKE_INSTALL_RPATH='\$ORIGIN/../lib' \
  -DCMAKE_BUILD_RPATH_USE_ORIGIN=ON \
  -DLLVM_ENABLE_PROJECTS="${LLVM_ENABLE_PROJECTS}" \
  -DLLVM_ENABLE_RUNTIMES="${LLVM_ENABLE_RUNTIMES}" \
  -DLLVM_TARGETS_TO_BUILD="${LLVM_TARGETS_TO_BUILD}" \
  -DLLVM_ENABLE_ZLIB=ON \
  -DLLVM_ENABLE_ZSTD=ON \
  -DLLVM_ENABLE_LIBXML2=ON \
  -DLLVM_INSTALL_UTILS=ON \
  -DLLVM_BUILD_TOOLS=ON \
  -DLLVM_BUILD_LLVM_DYLIB=ON \
  -DLLVM_LINK_LLVM_DYLIB=ON \
  -DLLVM_ENABLE_LLD=OFF \
  -DLLVM_INCLUDE_TESTS=OFF \
  -DLLVM_INCLUDE_EXAMPLES=OFF \
  -DLLVM_INCLUDE_BENCHMARKS=OFF \
  -DLLVM_INCLUDE_DOCS=OFF \
  -DCLANG_INCLUDE_TESTS=OFF \
  -DCLANG_DEFAULT_LINKER=lld \
  -DLLVM_PARALLEL_LINK_JOBS="${LLVM_PARALLEL_LINK_JOBS}" \
  -DLLVM_CCACHE_BUILD=ON \
  -DCOMPILER_RT_BUILD_LIBFUZZER=ON \
  -DCOMPILER_RT_BUILD_SANITIZERS=ON \
  -DCOMPILER_RT_BUILD_XRAY=ON \
  -DCOMPILER_RT_BUILD_PROFILE=ON \
  -DLIBCXX_USE_COMPILER_RT=ON \
  -DLIBCXXABI_USE_COMPILER_RT=ON \
  -DLIBCXXABI_USE_LLVM_UNWINDER=ON \
  -DLIBUNWIND_USE_COMPILER_RT=ON

echo "Building (this is intentionally huge)..."
cmake --build "${BUILD_DIR}" --parallel "${JOBS}"

echo "Installing into package prefix..."
cmake --install "${BUILD_DIR}"

# Free disk before packing
rm -rf "${BUILD_DIR}"
# Keep source out of artifact; drop it
rm -rf "${SRC_DIR}"

# Ensure bin/clang exists
if [[ ! -x "${PREFIX_DIR}/bin/clang" && ! -x "${PREFIX_DIR}/bin/clang-20" ]]; then
  # versioned only?
  if compgen -G "${PREFIX_DIR}/bin/clang-*" > /dev/null; then
    first="$(ls "${PREFIX_DIR}/bin"/clang-[0-9]* 2>/dev/null | head -1 || true)"
    if [[ -n "$first" && ! -e "${PREFIX_DIR}/bin/clang" ]]; then
      ln -sfn "$(basename "$first")" "${PREFIX_DIR}/bin/clang"
    fi
  fi
fi
if [[ ! -e "${PREFIX_DIR}/bin/clang" ]]; then
  echo "clang missing after install" >&2
  ls -la "${PREFIX_DIR}/bin" | head -50 >&2
  exit 1
fi

# Symlink common aliases if missing
for pair in "clang++:clang" "ld.lld:lld"; do
  dst="${pair%%:*}"; src="${pair##*:}"
  if [[ ! -e "${PREFIX_DIR}/bin/$dst" && -e "${PREFIX_DIR}/bin/$src" ]]; then
    ln -sfn "$src" "${PREFIX_DIR}/bin/$dst"
  fi
done

# Bundle non-system shared libs referenced by major binaries
mkdir -p "${PREFIX_DIR}/lib"
is_system_lib() {
  case "$1" in
    /lib/*|/lib64/*|/usr/lib/*|/usr/lib64/*) return 0 ;;
    *) return 1 ;;
  esac
}
declare -A SEEN_LIBS=()
copy_lib() {
  local src="$1"
  [[ -z "$src" || ! -e "$src" ]] && return 0
  local real
  real="$(readlink -f "$src" || true)"
  [[ -z "$real" || ! -f "$real" ]] && return 0
  [[ -n "${SEEN_LIBS[$real]+x}" ]] && return 0
  SEEN_LIBS[$real]=1
  is_system_lib "$real" && return 0
  local base; base="$(basename "$real")"
  case "$base" in ld-linux*.so*|ld-*.so) return 0 ;; esac
  echo "  bundling $real"
  cp -a "$real" "${PREFIX_DIR}/lib/" || true
  while read -r dep; do
    [[ -z "$dep" ]] && continue
    copy_lib "$dep"
  done < <(ldd "$real" 2>/dev/null | awk '/=> \// { print $3 }' || true)
}

for bin in "${PREFIX_DIR}/bin"/clang "${PREFIX_DIR}/bin"/clang++ "${PREFIX_DIR}/bin"/lld "${PREFIX_DIR}/bin"/lldb; do
  [[ -e "$bin" ]] || continue
  while read -r dep; do copy_lib "$dep"; done < <(ldd "$bin" 2>/dev/null | awk '/=> \// { print $3 }' || true)
  if command -v patchelf >/dev/null 2>&1 && [[ -f "$bin" && ! -L "$bin" ]]; then
    patchelf --set-rpath '$ORIGIN/../lib' "$bin" 2>/dev/null || true
  fi
done

# RPATH on dylibs we install under lib
if command -v patchelf >/dev/null 2>&1; then
  find "${PREFIX_DIR}/lib" -maxdepth 1 -type f -name '*.so*' 2>/dev/null | while read -r so; do
    patchelf --set-rpath '$ORIGIN' "$so" 2>/dev/null || true
  done
fi

cat > "${PREFIX_DIR}/BUILDINFO.txt" <<META
package=${PACKAGE_NAME}
version=${ARTIFACT_VERSION}
upstream_ref=${REF}
upstream_sha=${GIT_SHA}
build_target=${BUILD_TARGET}
projects=${LLVM_ENABLE_PROJECTS}
runtimes=${LLVM_ENABLE_RUNTIMES}
targets=${LLVM_TARGETS_TO_BUILD}
distributor=${DISTRIBUTOR}
built_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
META

# List key tools for the log
echo "--- bin (sample) ---"
ls "${PREFIX_DIR}/bin" | head -80

ARCHIVE_NAME="${PACKAGE_NAME}-${ARTIFACT_VERSION}-${ARCHIVE_SUFFIX}.tar.gz"
# Note: foundation FindTarballs matches version string in name; for trunk we pass version=trunk
# so also emit a stable alias when VERSION_RAW is trunk/main.
tar -czf "${OUTPUT_DIR}/${ARCHIVE_NAME}" -C "${STAGE_DIR}" "${PACKAGE_NAME}"
echo "Done: ${OUTPUT_DIR}/${ARCHIVE_NAME}"
ls -lh "${OUTPUT_DIR}/${ARCHIVE_NAME}"

if [[ "$VERSION_RAW" == "trunk" || "$VERSION_RAW" == "main" ]]; then
  ALIAS="${PACKAGE_NAME}-trunk-${ARCHIVE_SUFFIX}.tar.gz"
  cp -a "${OUTPUT_DIR}/${ARCHIVE_NAME}" "${OUTPUT_DIR}/${ALIAS}"
  echo "Alias: ${OUTPUT_DIR}/${ALIAS}"
fi
