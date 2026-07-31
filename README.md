# actions-precompiled / llvm-bin

Relocatable **LLVM kitchen-sink** toolchain built with
[`foundation`](https://github.com/actions-precompiled/foundation) (Cobra CLI).

**Tagged releases only** — CI plans the latest upstream release tag; publish
refuses `trunk`/`main`/`latest`.

**Self-contained Linux trees** (vendors libedit/ncurses/z/xml2/… into `lib/`) — post-install `patchelf` sets `$ORIGIN` RPATH; smoke runs with a clean loader env (no `LD_LIBRARY_PATH`, no package `PATH`).

## CLI

```bash
mise install
mise exec -- go run . plan                    # → latest llvmorg-* (push/PR default)
mise exec -- go run . list                    # missing tags (one per line)
mise exec -- go run . list --all
mise exec -- go run . build llvmorg-21.1.0    # host injects binary into Docker for linux-*
mise exec -- go run . smoke llvmorg-21.1.0
mise exec -- go run . generate workflow --force
```

### Architecture

| Layer | What runs |
|-------|-----------|
| Host | `go run . build <tag>` — plan, docker image, mount binary as `/apc`, smoke, publish |
| Container | `/apc work` — pure Go `Package.Work` |
| Windows | `Work` natively |

Dockerfile is **deps only** (no shell `ENTRYPOINT`).

### CI

- `build.yml` — `go run . plan` then matrix `go run . build <tag>`
- `dispatch-missing.yml` — `go run . list` → one Build per **tag**

## Targets

- `linux-amd64`, `linux-aarch64` — full projects + libc++/unwind
- `windows-amd64` — tools + `compiler-rt;openmp`; optional xwin

## License

MIT for packaging. LLVM is Apache-2.0 with LLVM exceptions.
