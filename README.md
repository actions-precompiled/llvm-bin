# actions-precompiled / llvm-bin

Relocatable **LLVM kitchen-sink** toolchain built with
[`foundation`](https://github.com/actions-precompiled/foundation) (Cobra CLI).

## CLI

```bash
mise install
mise exec -- go run . list                 # versions for dispatch (one per line)
mise exec -- go run . list --all
mise exec -- go run . build trunk          # host: injects this binary into Docker for linux-*
mise exec -- go run . smoke trunk
mise exec -- go run . generate workflow --force
```

### Architecture

| Layer | What runs |
|-------|-----------|
| Host | `go run . build <version>` — plan, docker image, **mount binary as `/apc`**, smoke, publish |
| Container | `/apc work` — pure Go `Package.Work` (cmake/ninja via Runner, no bash packaging script) |
| Windows | `Work` natively on the runner |

Dockerfile is **deps only** (no `ENTRYPOINT` script).

### CI

Generated workflows:

- `build.yml` — matrix from `Meta.DefaultTargets`; thin `go run . build`
- `dispatch-missing.yml` — `go run . list` → `gh workflow run Build` per version

```bash
go run . generate workflow --force
```

## Targets

- `linux-amd64`, `linux-aarch64` — full projects + libc++/unwind runtimes
- `windows-amd64` — same tools; runtimes `compiler-rt;openmp` (no libunwind on MSVC ABI); optional xwin sysroot

## License

MIT for packaging. LLVM is Apache-2.0 with LLVM exceptions.
