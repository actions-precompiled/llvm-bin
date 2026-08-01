package main

import (
	"context"
	"errors"
	"fmt"
	"os"
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

	// Wipe build tree but keep host preclone outside work/ when used.
	deps.RemoveAllLog(build, "remove")
	deps.RemoveAllLog(stage, "remove")
	for _, d := range []string{build, prefix, req.OutDir} {
		if err := deps.FS.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	src, ref, artifactVersion, sha, err := resolveSource(ctx, deps, meta, versionRaw, src)
	if err != nil {
		return err
	}
	deps.Logf("Resolved ref=%s sha=%s artifact=%s src=%s", ref, sha, artifactVersion, src)

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
	// libedit: vendor shared libs into prefix/lib after install (static .a breaks libLLVM.so PIC link).
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
	// Keep APC_PREBUILT_SRC / host cache; only drop ephemeral in-tree clone.
	if deps.Env.Get("APC_PREBUILT_SRC") == "" && !isUnderCache(src) {
		deps.RemoveAllLog(src, "remove")
	}

	// Ship libedit (and friends) inside the package when still dynamically linked.
	if err := embedLinuxHostLibs(ctx, deps, prefix); err != nil {
		return fmt.Errorf("embed host libs: %w", err)
	}

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

// embedLinuxHostLibs copies selected host shared libs into prefix/lib when any
// installed ELF still DT_NEEDs them (static link failed or other tools need them).
// Then RPATH/$ORIGIN resolves them without distro packages.
func embedLinuxHostLibs(ctx context.Context, deps foundation.Deps, prefix string) error {
	// Third-party libs we ship inside the package (not glibc/libstdc++/libgcc).
	// Static libedit is preferred when available; anything still DT_NEEDED is copied.
	wantPrefixes := []string{
		"libedit.so",
		"libtinfo.so",
		"libncurses.so",
		"libncursesw.so",
		"libpanel.so",
		"libpanelw.so",
		"libform.so",
		"libformw.so",
		"libz.so",
		"libzstd.so",
		"libxml2.so",
		"liblzma.so",
		"libcurl.so",
		"libssl.so",
		"libcrypto.so",
		"libffi.so",
		"libssh2.so",
		"libnghttp2.so",
		"libpsl.so",
		"libidn2.so",
		"libunistring.so",
		"libbrotlidec.so",
		"libbrotlicommon.so",
	}
	libDir := filepath.Join(prefix, "lib")
	if err := deps.FS.MkdirAll(libDir, 0o755); err != nil {
		return err
	}
	needed := map[string]struct{}{}
	// Scan bin/ and lib/ for DT_NEEDED of interest via readelf or ldd.
	for _, sub := range []string{"bin", "lib"} {
		root := filepath.Join(prefix, sub)
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d == nil || d.IsDir() {
				return nil
			}
			out, err := deps.Runner.Output(ctx, "bash", "-c",
				"readelf -d "+shellQuote(path)+" 2>/dev/null | sed -n 's/.*Shared library: \\[\\(.*\\)\\]/\\1/p'")
			if err != nil || strings.TrimSpace(out) == "" {
				return nil
			}
			for _, line := range strings.Split(out, "\n") {
				soname := strings.TrimSpace(line)
				for _, pref := range wantPrefixes {
					if soname == pref || strings.HasPrefix(soname, pref+".") || strings.HasPrefix(soname, pref) {
						needed[soname] = struct{}{}
					}
				}
			}
			return nil
		})
	}
	if len(needed) == 0 {
		deps.Logf("embed: no vendorable third-party DT_NEEDED left")
		return nil
	}
	for soname := range needed {
		// already in package?
		if findInTree(prefix, soname) != "" {
			continue
		}
		src, err := resolveHostLib(ctx, deps, soname)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", soname, err)
		}
		dst := filepath.Join(libDir, filepath.Base(src))
		if err := deps.Runner.Run(ctx, "cp", "-a", src, dst); err != nil {
			return fmt.Errorf("copy %s: %w", src, err)
		}
		// also copy real file if symlink
		if target, err := os.Readlink(src); err == nil && !filepath.IsAbs(target) {
			real := filepath.Join(filepath.Dir(src), target)
			if st, err := os.Stat(real); err == nil && !st.IsDir() {
				_ = deps.Runner.Run(ctx, "cp", "-a", real, filepath.Join(libDir, filepath.Base(real)))
			}
		}
		deps.Logf("embed: %s -> lib/%s", src, filepath.Base(dst))
	}
	return nil
}

func findInTree(prefix, base string) string {
	var found string
	_ = filepath.WalkDir(prefix, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if d.Name() == base {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func resolveHostLib(ctx context.Context, deps foundation.Deps, soname string) (string, error) {
	// ldconfig -p | grep soname
	out, err := deps.Runner.Output(ctx, "bash", "-c",
		"ldconfig -p 2>/dev/null | awk -v s="+shellQuote(soname)+` 'index($1,s)==1 {print $NF; exit}'`)
	if err == nil {
		p := strings.TrimSpace(out)
		if p != "" {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	// fallback paths
	for _, dir := range []string{
		"/lib/" + runtime.GOARCH + "-linux-gnu",
		"/usr/lib/" + runtime.GOARCH + "-linux-gnu",
		"/lib/x86_64-linux-gnu",
		"/usr/lib/x86_64-linux-gnu",
		"/lib/aarch64-linux-gnu",
		"/usr/lib/aarch64-linux-gnu",
		"/lib64",
		"/usr/lib64",
		"/lib",
		"/usr/lib",
	} {
		cand := filepath.Join(dir, soname)
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}
	return "", fmt.Errorf("host library %s not found", soname)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func isUnderCache(src string) bool {
	s := filepath.ToSlash(src)
	return strings.Contains(s, "/.cache/src/") || s == "/src" || strings.HasPrefix(s, "/src/")
}
