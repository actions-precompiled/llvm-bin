package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/actions-precompiled/foundation"
)

// Package-level smoke is a molecule: extract + platform policy on top of
// foundation atoms (CleanSmokeEnv, OutputWithEnv, CheckLinuxRelocatable, RemoveAllLog).

func smokeLinux(ctx context.Context, deps foundation.Deps, meta foundation.Meta, req foundation.SmokeRequest) error {
	return smokeEachTarball(req, func(tb string) error {
		return smokeLinuxTarball(ctx, deps, meta, tb)
	})
}

func smokeWindows(ctx context.Context, deps foundation.Deps, meta foundation.Meta, req foundation.SmokeRequest) error {
	return smokeEachTarball(req, func(tb string) error {
		return smokeWindowsTarball(ctx, deps, meta, tb)
	})
}

func smokeEachTarball(req foundation.SmokeRequest, fn func(tarball string) error) error {
	if len(req.Tarballs) == 0 {
		return fmt.Errorf("%w", ErrSmokeNoTarballs)
	}
	for _, tb := range req.Tarballs {
		if err := fn(tb); err != nil {
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
	defer deps.RemoveAllLog(tmp, "smoke cleanup")

	if err := deps.Runner.Run(ctx, "tar", "-xzf", tarball, "-C", tmp); err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	root := filepath.Join(tmp, meta.Name)
	clang := filepath.Join(root, "bin", "clang")
	if _, err := deps.FS.Stat(clang); err != nil {
		return fmt.Errorf("missing bin/clang: %w", err)
	}

	if err := foundation.CheckLinuxRelocatable(root, foundation.RelocatableOpts{
		RequiredBins: []string{"clang"},
	}); err != nil {
		return err
	}
	deps.Logf("relocatable: RPATH/$ORIGIN OK")

	env := foundation.CleanSmokeEnv(deps.Env.Environ())

	if out, err := foundation.OutputWithEnv(ctx, deps, env, clang, "--version"); err != nil {
		return fmt.Errorf("clang --version: %w\n%s", err, out)
	} else {
		deps.Logf("%s", firstLines(out, 4))
	}

	hello := filepath.Join(tmp, "hello.c")
	if err := deps.FS.WriteFile(hello, []byte("#include <stdio.h>\nint main(void){puts(\"hi\");return 0;}\n"), 0o644); err != nil {
		return err
	}
	bin := filepath.Join(tmp, "hello")
	lld := filepath.Join(root, "bin", "lld")
	args := []string{"-fuse-ld=lld", "-o", bin, hello}
	if _, err := deps.FS.Stat(lld); err == nil {
		args = []string{"-fuse-ld=" + lld, "-o", bin, hello}
	}
	if out, err := foundation.OutputWithEnv(ctx, deps, env, clang, args...); err != nil {
		return fmt.Errorf("compile hello: %w\n%s", err, out)
	}
	if out, err := foundation.OutputWithEnv(ctx, deps, env, bin); err != nil {
		return fmt.Errorf("run hello: %w\n%s", err, out)
	} else if !strings.Contains(out, "hi") {
		return fmt.Errorf("%w: %q", ErrHelloOutput, out)
	}

	for _, util := range []string{"clang++", "clang-format", "clang-tidy", "clangd", "lld", "llvm-ar", "llvm-config", "lldb"} {
		p := filepath.Join(root, "bin", util)
		if _, err := deps.FS.Stat(p); err != nil {
			deps.Logf("WARN missing optional util: %s", util)
			continue
		}
		out, err := foundation.OutputWithEnv(ctx, deps, env, p, "--version")
		if err != nil {
			out2, err2 := foundation.OutputWithEnv(ctx, deps, env, p, "-version")
			if err2 != nil {
				return fmt.Errorf("%w (%s): %w", ErrUtilVersion, util, errors.Join(err, err2))
			}
			out = out2
		}
		deps.Logf("ok %s: %s", util, firstLines(out, 1))
	}

	deps.Logf("✓ Smoke test passed: %s", filepath.Base(tarball))
	return nil
}

func smokeWindowsTarball(ctx context.Context, deps foundation.Deps, meta foundation.Meta, tarball string) error {
	deps.Logf("Smoke test (windows self-host): %s", filepath.Base(tarball))
	tmp, err := deps.FS.TempDir("", "llvm-smoke-win-")
	if err != nil {
		return err
	}
	defer deps.RemoveAllLog(tmp, "smoke cleanup")

	if err := deps.Runner.Run(ctx, "tar", "-xzf", tarball, "-C", tmp); err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	root := filepath.Join(tmp, meta.Name)
	clang := filepath.Join(root, "bin", "clang.exe")
	if _, err := deps.FS.Stat(clang); err != nil {
		clang = filepath.Join(root, "bin", "clang")
		if _, err2 := deps.FS.Stat(clang); err2 != nil {
			return fmt.Errorf("%w: %w", ErrMissingClang, errors.Join(err, err2))
		}
	}

	env := foundation.CleanSmokeEnv(deps.Env.Environ())
	sysroot := filepath.Join(root, "xwin")
	if st, err := deps.FS.Stat(sysroot); err == nil && st.IsDir() {
		env = setEnvList(env, "APCLLVM_XWIN", sysroot)
	}

	if out, err := foundation.OutputWithEnv(ctx, deps, env, clang, "--version"); err != nil {
		return fmt.Errorf("clang --version: %w\n%s", err, out)
	} else {
		deps.Logf("%s", firstLines(out, 4))
	}

	hello := filepath.Join(tmp, "hello.c")
	if err := deps.FS.WriteFile(hello, []byte("#include <stdio.h>\nint main(void){puts(\"hi\");return 0;}\n"), 0o644); err != nil {
		return err
	}
	outExe := filepath.Join(tmp, "hello.exe")

	var args []string
	if _, err := deps.FS.Stat(sysroot); err == nil {
		args = []string{
			"-target", "x86_64-pc-windows-msvc",
			"-fuse-ld=lld",
			"--sysroot=" + sysroot,
			"-o", outExe, hello,
		}
	} else {
		args = []string{"-fuse-ld=lld", "-o", outExe, hello}
	}
	if out, err := foundation.OutputWithEnv(ctx, deps, env, clang, args...); err != nil {
		return fmt.Errorf("compile hello (windows): %w\n%s", err, out)
	}
	if out, err := foundation.OutputWithEnv(ctx, deps, env, outExe); err != nil {
		return fmt.Errorf("run hello.exe: %w\n%s", err, out)
	}
	deps.Logf("✓ Windows smoke passed: %s", filepath.Base(tarball))
	return nil
}

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

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
