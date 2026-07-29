# actions-precompiled / llvm

**Tech demo** relocatable **LLVM kitchen-sink** toolchain for mise/tarball installs.

Built with [`actions-precompiled/foundation`](https://github.com/actions-precompiled/foundation) (Go `Package` interface, no `package.toml` magic).

## What’s in the box

| Area | Contents |
|------|----------|
| Compiler | `clang`, `clang++`, default linker **lld** |
| Tools | `clang-format`, `clang-tidy`, `clangd`, clang-tools-extra |
| LLVM utils | `llvm-ar`, `llvm-objcopy`, `llvm-symbolizer`, `llvm-config`, … |
| Extra projects | **lldb**, **mlir**, **polly**, **bolt**, **openmp** |
| Runtimes | **compiler-rt** (sanitizers, profile, fuzzer), **libc++ / libc++abi / libunwind** |
| Backends | `LLVM_TARGETS_TO_BUILD=Native` by default (override for more) |

Windows artifacts additionally try to ship an **[xwin](https://github.com/Jake-Shadle/xwin)** splat of MSVC CRT + SDK under `xwin/`, so **consumers can compile without `cl.exe` or MinGW** (builder still bootstraps with VS on GHA). Redistribution of those bits is subject to Microsoft’s terms.

## Versioning

| Input | Meaning |
|-------|---------|
| `trunk` / `main` | Tip of llvm-project main → artifact `llvm-trunk-<sha>-…` (+ `llvm-trunk-…` alias) |
| `llvmorg-X.Y.Z` | Upstream release tag |

Primary mode: **trunk + babysit** (nightly workflow + long CI). Compute/disk pressure is expected.

## Local (Linux, needs Docker)

```bash
mise install
mise exec -- go run . --dry-run trunk
# multi-hour:
APC_TARGETS=linux-amd64 mise exec -- go run . trunk
```

## CI

Thin matrix: checkout → mise → `go run . <version>` with `APC_TARGETS`.
Host prep (disk free, choco cmake/ninja, VsDevCmd) is in the Go payload /
`scripts/` via `runtime.GOOS`, not workflow `if:` branches.

## CI details

| Workflow | Role |
|----------|------|
| `Build` | push/PR → trunk; dispatch → any ref; optional publish |
| `Babysit trunk` | daily `workflow_dispatch` of Build@trunk |

Timeouts: **360 minutes** per job. Disk free-up step on Linux runners.

## Layout

```text
llvm-<version>-linux-amd64.tar.gz
└── llvm/
    ├── bin/clang, clang++, lld, lldb, clangd, …
    ├── lib/   # LLVM dylibs + bundled deps
    ├── include/, libexec/, share/, …
    └── BUILDINFO.txt
```

Windows adds `xwin/` + `WINDOWS_SELFHOST.txt` + `bin/activate-xwin.cmd` when xwin succeeds.

## Honest limits

- Free GHA runners may **OOM or run out of disk** on full sink builds — babysit, narrow `LLVM_ENABLE_PROJECTS` / `LLVM_TARGETS_TO_BUILD`, or use larger runners if needed.
- Windows self-host is **best-effort** via xwin; some C++ features still assume a normal Windows environment (UCRT on Win10+).
- This is a **tech demo**, not a commitment to ship every LLVM release forever.

## License

MIT for packaging scripts. LLVM is Apache-2.0 with LLVM exceptions; xwin-fetched CRT/SDK follows Microsoft licensing.
