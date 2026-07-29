package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/actions-precompiled/foundation"
)

// workLinux builds LLVM inside the container (or any Linux host with deps).
// Invoked as: /apc work  (binary mounted by foundation host build).
func workLinux(ctx context.Context, deps foundation.Deps, meta foundation.Meta, req foundation.BuildRequest) error {
	versionRaw := req.Version
	archiveSuffix, err := linuxArchiveSuffix(req.Target)
	if err != nil {
		return err
	}

	projects := envOr(deps, "LLVM_ENABLE_PROJECTS", "clang;clang-tools-extra;lld;lldb;mlir;polly;bolt")
	runtimes := envOr(deps, "LLVM_ENABLE_RUNTIMES", "compiler-rt;libcxx;libcxxabi;libunwind;openmp")
	targets := envOr(deps, "LLVM_TARGETS_TO_BUILD", "Native")
	linkJobs := envOr(deps, "LLVM_PARALLEL_LINK_JOBS", "2")
	jobs := envOr(deps, "JOBS", strconv.Itoa(runtime.NumCPU()))

	work := filepath.Join("/tmp", meta.Name+"-build")
	src := filepath.Join(work, "src")
	build := filepath.Join(work, "build")
	stage := filepath.Join(work, "stage")
	prefix := filepath.Join(stage, meta.Name)

	deps.RemoveAllLog(work, "remove")
	for _, d := range []string{src, build, prefix, req.OutDir} {
		if err := deps.FS.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	ref, artifactVersion, sha, err := cloneUpstream(ctx, deps, meta.UpstreamGit, versionRaw, src)
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
		// Passed via exec (no shell): literal $ORIGIN for the dynamic linker.
		"-DCMAKE_INSTALL_RPATH=$ORIGIN/../lib",
		"-DCMAKE_BUILD_RPATH_USE_ORIGIN=ON",
		"-DCMAKE_BUILD_WITH_INSTALL_RPATH=ON",
		"-DCMAKE_INSTALL_RPATH_USE_LINK_PATH=OFF",
		"-DLLVM_ENABLE_PROJECTS=" + projects,
		"-DLLVM_ENABLE_RUNTIMES=" + runtimes,
		"-DLLVM_TARGETS_TO_BUILD=" + targets,
		"-DLLVM_ENABLE_ZLIB=ON",
		"-DLLVM_ENABLE_ZSTD=ON",
		"-DLLVM_ENABLE_LIBXML2=ON",
		"-DLLVM_INSTALL_UTILS=ON",
		"-DLLVM_BUILD_TOOLS=ON",
		"-DLLVM_BUILD_LLVM_DYLIB=ON",
		"-DLLVM_LINK_LLVM_DYLIB=ON",
		"-DLLVM_INCLUDE_TESTS=OFF",
		"-DLLVM_INCLUDE_EXAMPLES=OFF",
		"-DLLVM_INCLUDE_BENCHMARKS=OFF",
		"-DLLVM_INCLUDE_DOCS=OFF",
		"-DCLANG_INCLUDE_TESTS=OFF",
		"-DCLANG_DEFAULT_LINKER=lld",
		"-DLLVM_PARALLEL_LINK_JOBS=" + linkJobs,
		"-DLLVM_CCACHE_BUILD=ON",
		"-DLIBCXX_USE_COMPILER_RT=ON",
		"-DLIBCXXABI_USE_COMPILER_RT=ON",
		"-DLIBCXXABI_USE_LLVM_UNWINDER=ON",
		"-DLIBUNWIND_USE_COMPILER_RT=ON",
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

	deps.RemoveAllLog(build, "remove")
	deps.RemoveAllLog(src, "remove")

	if err := ensureClangSymlinks(ctx, deps, prefix); err != nil {
		return err
	}
	// Belt-and-suspenders: force $ORIGIN rpaths so the tree is self-contained
	// without LD_LIBRARY_PATH (CMake alone is not always enough for every tool).
	if err := foundation.PatchLinuxOriginRPath(ctx, deps, prefix); err != nil {
		return fmt.Errorf("relocatable rpath: %w", err)
	}
	if err := foundation.CheckLinuxRelocatable(prefix, foundation.RelocatableOpts{
		RequiredBins: []string{"clang"},
	}); err != nil {
		return fmt.Errorf("post-install relocatable check: %w", err)
	}

	info := fmt.Sprintf(`package=%s
version=%s
upstream_ref=%s
upstream_sha=%s
build_target=%s
projects=%s
runtimes=%s
targets=%s
distributor=actions-precompiled
built_at=%s
`, meta.Name, artifactVersion, ref, sha, req.Target, projects, runtimes, targets, time.Now().UTC().Format(time.RFC3339))
	if err := deps.FS.WriteFile(filepath.Join(prefix, "BUILDINFO.txt"), []byte(info), 0o644); err != nil {
		return err
	}

	archive := filepath.Join(req.OutDir, foundation.ArtifactName(meta.Name, artifactVersion, archiveSuffix))
	// tar from stage
	if err := deps.Runner.Run(ctx, "tar", "-czf", archive, "-C", stage, meta.Name); err != nil {
		return fmt.Errorf("tar: %w", err)
	}
	deps.Logf("Done: %s", archive)

	return nil
}

func linuxArchiveSuffix(target string) (string, error) {
	switch target {
	case foundation.TargetLinuxAMD64, "linux-x86_64":
		return foundation.TargetLinuxAMD64, nil
	case foundation.TargetLinuxAArch64, "linux-arm64":
		return foundation.TargetLinuxAArch64, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedTarget, target)
	}
}

func cloneUpstream(ctx context.Context, deps foundation.Deps, upstream, versionRaw, src string) (ref, artifact, sha string, err error) {
	tryClone := func(branch string) error {
		deps.RemoveAllLog(src, "remove")
		if err := deps.FS.MkdirAll(src, 0o755); err != nil {
			return err
		}
		// git clone into src - need empty or clone creates dir
		deps.RemoveAllLog(src, "remove")
		return deps.Runner.Run(ctx, "git", "clone", "--depth", "1", "--branch", branch, upstream, src)
	}

	switch {
	case versionRaw == "trunk" || versionRaw == "main" || versionRaw == "git-main" || strings.HasPrefix(versionRaw, "trunk-"):
		ref = "main"
		if err := tryClone("main"); err != nil {
			ref = "master"
			if err2 := tryClone("master"); err2 != nil {
				return "", "", "", fmt.Errorf("%w: %w", ErrCloneTrunk, errors.Join(err, err2))
			}
		}
	default:
		ref = versionRaw
		if err := tryClone(ref); err != nil {
			if !strings.HasPrefix(versionRaw, "llvmorg-") {
				ref = "llvmorg-" + strings.TrimPrefix(versionRaw, "v")
				if err2 := tryClone(ref); err2 != nil {
					return "", "", "", fmt.Errorf("clone %s: %w", versionRaw, err)
				}
			} else {
				return "", "", "", err
			}
		}
	}

	out, err := deps.Runner.Output(ctx, "git", "-C", src, "rev-parse", "--short=12", "HEAD")
	if err != nil {
		return "", "", "", err
	}
	sha = strings.TrimSpace(out)
	if versionRaw == "trunk" || versionRaw == "main" || strings.HasPrefix(versionRaw, "trunk-") {
		artifact = "trunk-" + sha
	} else if strings.HasPrefix(versionRaw, "llvmorg-") {
		artifact = versionRaw
	} else {
		artifact = foundation.VersionBare(versionRaw)
	}
	return ref, artifact, sha, nil
}

func ensureClangSymlinks(ctx context.Context, deps foundation.Deps, prefix string) error {
	bin := filepath.Join(prefix, "bin")
	clang := filepath.Join(bin, "clang")
	if _, err := deps.FS.Stat(clang); err != nil {
		// try versioned
		entries, _ := deps.FS.ReadDir(bin)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "clang-") && !strings.Contains(e.Name(), ".") {
				if err := deps.Runner.Run(ctx, "ln", "-sfn", e.Name(), clang); err != nil {
					return fmt.Errorf("symlink clang: %w", err)
				}
				break
			}
		}
	}
	if _, err := deps.FS.Stat(clang); err != nil {
		return fmt.Errorf("%w under %s", ErrClangMissingInstall, bin)
	}
	return nil
}

func envOr(deps foundation.Deps, key, def string) string {
	if v := deps.Env.Get(key); v != "" {
		return v
	}
	return def
}
