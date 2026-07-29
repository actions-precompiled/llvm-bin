package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/actions-precompiled/foundation"
)

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// buildWindowsNative runs the PowerShell (or bash) host build on a Windows runner.
// Bootstrap may use MSVC on the builder; the shipped kit aims to compile without cl/mingw
// by bundling clang, lld, runtimes, and an xwin-fetched MSVC/ucrt sysroot.

func setEnvList(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func smokeLinux(ctx context.Context, deps foundation.Deps, meta foundation.Meta, req foundation.SmokeRequest) error {
	// Richer than default: clang -v, compile hello with lld, clang-format, lld version.
	if len(req.Tarballs) == 0 {
		return fmt.Errorf("smoke: no tarballs")
	}
	for _, tb := range req.Tarballs {
		if err := smokeLinuxTarball(ctx, deps, meta, tb); err != nil {
			return err
		}
	}
	return nil
}

func smokeLinuxTarball(ctx context.Context, deps foundation.Deps, meta foundation.Meta, tarball string) error {
	deps.Logf("Smoke test (linux kitchen sink): %s", filepath.Base(tarball))
	tmp, err := deps.FS.TempDir("", "llvm-smoke-")
	if err != nil {
		return err
	}
	defer func() { _ = deps.FS.RemoveAll(tmp) }()

	if err := deps.Runner.Run(ctx, "tar", "-xzf", tarball, "-C", tmp); err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	root := filepath.Join(tmp, meta.Name)
	clang := filepath.Join(root, "bin", "clang")
	if _, err := deps.FS.Stat(clang); err != nil {
		return fmt.Errorf("missing bin/clang: %w", err)
	}

	// Prefer package lib over host.
	lib := filepath.Join(root, "lib")
	env := deps.Env.Environ()
	env = setEnvList(env, "LD_LIBRARY_PATH", lib+pathListSep()+os.Getenv("LD_LIBRARY_PATH"))
	env = setEnvList(env, "PATH", filepath.Join(root, "bin")+pathListSep()+os.Getenv("PATH"))

	run := func(name string, args ...string) (string, error) {
		if rw, ok := deps.Runner.(foundation.RunnerWithOpts); ok {
			return rw.OutputWith(ctx, foundation.RunOpts{Env: env}, name, args...)
		}
		return deps.Runner.Output(ctx, name, args...)
	}

	if out, err := run(clang, "--version"); err != nil {
		return fmt.Errorf("clang --version: %w\n%s", err, out)
	} else {
		deps.Logf("%s", firstLines(out, 4))
	}

	// Compile a tiny C program with the bundled lld.
	hello := filepath.Join(tmp, "hello.c")
	if err := deps.FS.WriteFile(hello, []byte("#include <stdio.h>\nint main(void){puts(\"hi\");return 0;}\n"), 0o644); err != nil {
		return err
	}
	bin := filepath.Join(tmp, "hello")
	if out, err := run(clang, "-fuse-ld=lld", "-o", bin, hello); err != nil {
		return fmt.Errorf("compile hello: %w\n%s", err, out)
	}
	if out, err := run(bin); err != nil {
		return fmt.Errorf("run hello: %w\n%s", err, out)
	} else if !strings.Contains(out, "hi") {
		return fmt.Errorf("hello output unexpected: %q", out)
	}

	// Spot-check utilities that define the "kitchen sink".
	for _, util := range []string{"clang++", "clang-format", "clang-tidy", "clangd", "lld", "llvm-ar", "llvm-config", "lldb"} {
		p := filepath.Join(root, "bin", util)
		if _, err := deps.FS.Stat(p); err != nil {
			deps.Logf("WARN missing optional util: %s", util)
			continue
		}
		// --version or -version
		if out, err := run(p, "--version"); err != nil {
			if out2, err2 := run(p, "-version"); err2 != nil {
				deps.Logf("WARN %s version failed: %v / %v", util, err, err2)
			} else {
				deps.Logf("ok %s: %s", util, firstLines(out2, 1))
			}
		} else {
			deps.Logf("ok %s: %s", util, firstLines(out, 1))
		}
	}

	deps.Logf("✓ Smoke test passed: %s", filepath.Base(tarball))
	return nil
}

func smokeWindows(ctx context.Context, deps foundation.Deps, meta foundation.Meta, req foundation.SmokeRequest) error {
	if len(req.Tarballs) == 0 {
		return fmt.Errorf("smoke: no tarballs")
	}
	for _, tb := range req.Tarballs {
		if err := smokeWindowsTarball(ctx, deps, meta, tb); err != nil {
			return err
		}
	}
	return nil
}

func smokeWindowsTarball(ctx context.Context, deps foundation.Deps, meta foundation.Meta, tarball string) error {
	deps.Logf("Smoke test (windows self-host): %s", filepath.Base(tarball))
	tmp, err := deps.FS.TempDir("", "llvm-smoke-win-")
	if err != nil {
		return err
	}
	defer func() { _ = deps.FS.RemoveAll(tmp) }()

	// tar is available on modern Windows / GHA
	if err := deps.Runner.Run(ctx, "tar", "-xzf", tarball, "-C", tmp); err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	root := filepath.Join(tmp, meta.Name)
	clang := filepath.Join(root, "bin", "clang.exe")
	if _, err := deps.FS.Stat(clang); err != nil {
		// some layouts omit .exe in path checks on wine — try without
		clang = filepath.Join(root, "bin", "clang")
		if _, err2 := deps.FS.Stat(clang); err2 != nil {
			return fmt.Errorf("missing bin/clang: %v / %v", err, err2)
		}
	}

	env := deps.Env.Environ()
	env = setEnvList(env, "PATH", filepath.Join(root, "bin")+pathListSep()+os.Getenv("PATH"))
	// xwin sysroot if packaged
	sysroot := filepath.Join(root, "xwin")
	if st, err := deps.FS.Stat(sysroot); err == nil && st.IsDir() {
		env = setEnvList(env, "APCLLVM_XWIN", sysroot)
	}

	run := func(name string, args ...string) (string, error) {
		if rw, ok := deps.Runner.(foundation.RunnerWithOpts); ok {
			return rw.OutputWith(ctx, foundation.RunOpts{Env: env}, name, args...)
		}
		return deps.Runner.Output(ctx, name, args...)
	}

	if out, err := run(clang, "--version"); err != nil {
		return fmt.Errorf("clang --version: %w\n%s", err, out)
	} else {
		deps.Logf("%s", firstLines(out, 4))
	}

	hello := filepath.Join(tmp, "hello.c")
	_ = deps.FS.WriteFile(hello, []byte("#include <stdio.h>\nint main(void){puts(\"hi\");return 0;}\n"), 0o644)
	outExe := filepath.Join(tmp, "hello.exe")

	// Prefer self-contained flags when xwin sysroot present.
	var args []string
	if _, err := deps.FS.Stat(sysroot); err == nil {
		args = []string{
			"-target", "x86_64-pc-windows-msvc",
			"-fuse-ld=lld",
			"--sysroot=" + sysroot,
			"-o", outExe, hello,
		}
	} else {
		// Fall back: hope runner has SDK (less ideal)
		args = []string{"-fuse-ld=lld", "-o", outExe, hello}
	}
	if out, err := run(clang, args...); err != nil {
		return fmt.Errorf("compile hello (windows): %w\n%s", err, out)
	}
	if out, err := run(outExe); err != nil {
		return fmt.Errorf("run hello.exe: %w\n%s", err, out)
	}
	deps.Logf("✓ Windows smoke passed: %s", filepath.Base(tarball))
	return nil
}

func pathListSep() string {
	if runtime.GOOS == "windows" {
		return ";"
	}
	return ":"
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
