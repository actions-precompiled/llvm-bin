package main

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/actions-precompiled/foundation"
)

func workWindows(ctx context.Context, deps foundation.Deps, meta foundation.Meta, req foundation.BuildRequest) error {
	if err := enterVsDevShell(ctx, deps, req.Target); err != nil {
		deps.Logf("VsDevShell: %v (continuing if cl is already on PATH)", err)
	}

	archiveSuffix := req.Target
	projects := envOr(deps, "LLVM_ENABLE_PROJECTS", "clang;clang-tools-extra;lld;lldb;mlir;polly;bolt")
	// MSVC: no libunwind/libc++
	runtimes := envOr(deps, "LLVM_ENABLE_RUNTIMES", "compiler-rt;openmp")
	targets := envOr(deps, "LLVM_TARGETS_TO_BUILD", "Native")
	linkJobs := envOr(deps, "LLVM_PARALLEL_LINK_JOBS", "1")
	jobs := envOr(deps, "JOBS", strconv.Itoa(runtime.NumCPU()))

	tmp := deps.Env.Get("TEMP")
	if tmp == "" {
		tmp = deps.Env.Get("TMP")
	}
	if tmp == "" {
		tmp = osTemp()
	}
	work := filepath.Join(tmp, "llvm-build")
	src := filepath.Join(work, "src")
	build := filepath.Join(work, "build")
	stage := filepath.Join(work, "stage")
	prefix := filepath.Join(stage, meta.Name)

	_ = deps.FS.RemoveAll(work)
	for _, d := range []string{src, build, prefix, req.OutDir} {
		if err := deps.FS.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	ref, artifactVersion, sha, err := cloneUpstream(ctx, deps, meta.UpstreamGit, req.Version, src)
	if err != nil {
		return err
	}
	deps.Logf("Resolved ref=%s sha=%s artifact=%s", ref, sha, artifactVersion)

	cmakeArgs := []string{
		"-G", "Ninja",
		"-S", filepath.Join(src, "llvm"),
		"-B", build,
		"-DCMAKE_BUILD_TYPE=Release",
		"-DCMAKE_INSTALL_PREFIX=" + prefix,
		"-DLLVM_ENABLE_PROJECTS=" + projects,
		"-DLLVM_ENABLE_RUNTIMES=" + runtimes,
		"-DLLVM_TARGETS_TO_BUILD=" + targets,
		"-DLLVM_ENABLE_ZLIB=ON",
		"-DLLVM_INSTALL_UTILS=ON",
		"-DLLVM_BUILD_TOOLS=ON",
		"-DLLVM_INCLUDE_TESTS=OFF",
		"-DLLVM_INCLUDE_EXAMPLES=OFF",
		"-DLLVM_INCLUDE_BENCHMARKS=OFF",
		"-DLLVM_INCLUDE_DOCS=OFF",
		"-DCLANG_INCLUDE_TESTS=OFF",
		"-DCLANG_DEFAULT_LINKER=lld",
		"-DLLVM_PARALLEL_LINK_JOBS=" + linkJobs,
	}
	if err := deps.Runner.Run(ctx, "cmake", cmakeArgs...); err != nil {
		return fmt.Errorf("cmake configure: %w", err)
	}
	if err := deps.Runner.Run(ctx, "cmake", "--build", build, "--parallel", jobs); err != nil {
		return fmt.Errorf("cmake build: %w", err)
	}
	if err := deps.Runner.Run(ctx, "cmake", "--install", build); err != nil {
		return fmt.Errorf("cmake install: %w", err)
	}
	_ = deps.FS.RemoveAll(build)
	_ = deps.FS.RemoveAll(src)

	// optional xwin
	if deps.Env.Get("SKIP_XWIN") != "1" {
		if err := splatXwin(ctx, deps, prefix, req.Target); err != nil {
			deps.Logf("xwin: %v (continuing)", err)
		}
	}

	info := fmt.Sprintf("package=%s\nversion=%s\nupstream_ref=%s\nupstream_sha=%s\nbuild_target=%s\nprojects=%s\nruntimes=%s\n",
		meta.Name, artifactVersion, ref, sha, req.Target, projects, runtimes)
	info += "built_at=" + time.Now().UTC().Format(time.RFC3339) + "\n"
	_ = deps.FS.WriteFile(filepath.Join(prefix, "BUILDINFO.txt"), []byte(info), 0o644)

	archive := filepath.Join(req.OutDir, foundation.ArtifactName(meta.Name, artifactVersion, archiveSuffix))
	// Windows tar
	if err := deps.Runner.Run(ctx, "tar", "-czf", archive, "-C", stage, meta.Name); err != nil {
		return fmt.Errorf("tar: %w", err)
	}
	deps.Logf("Done: %s", archive)
	return nil
}

func enterVsDevShell(ctx context.Context, deps foundation.Deps, target string) error {
	// Best-effort: invoke VsDevCmd via cmd and re-run is hard in one process.
	// Rely on GHA MSVC or user environment; PrepHost already installed cmake/ninja.
	// Try to locate vswhere and print hint.
	_, err := deps.Runner.Output(ctx, "where", "cl")
	if err == nil {
		return nil
	}
	return fmt.Errorf("cl.exe not on PATH; run from VS dev shell or GHA msvc-dev-cmd")
}

func splatXwin(ctx context.Context, deps foundation.Deps, prefix, target string) error {
	// Keep thin: skip full xwin download in pure-Go path for now if SKIP; host prep may set it.
	// Call out to xwin when present on PATH.
	arch := "x86_64"
	if strings.Contains(target, "arm64") || strings.Contains(target, "aarch64") {
		arch = "aarch64"
	}
	sysroot := filepath.Join(prefix, "xwin")
	if err := deps.FS.MkdirAll(sysroot, 0o755); err != nil {
		return err
	}
	// Prefer xwin on PATH (installed by PrepHost optional future)
	if err := deps.Runner.Run(ctx, "xwin", "splat", "--output", sysroot, "--arch", arch); err != nil {
		return err
	}
	return nil
}

func osTemp() string {
	return filepath.Join(".", "tmp-llvm-build")
}
