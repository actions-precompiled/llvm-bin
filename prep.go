package main

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/actions-precompiled/foundation"
)

// PrepHost implements foundation.HostPrep. CI only runs `go run`; OS weirdness lives here.
func (llvmPackage) PrepHost(ctx context.Context, deps foundation.Deps, cfg foundation.Config) error {
	switch runtime.GOOS {
	case "windows":
		return prepWindowsHost(ctx, deps)
	default:
		return prepLinuxHost(ctx, deps)
	}
}

func prepLinuxHost(ctx context.Context, deps foundation.Deps) error {
	// Free GHA disk when we are about to do a huge docker+llvm build.
	if deps.Env.Get("GITHUB_ACTIONS") == "" && !foundation.EnvFlag(deps.Env, "APC_FREE_DISK") {
		return nil
	}
	deps.Logf("PrepHost(linux): freeing disk for LLVM kitchen-sink build...")
	// Best-effort; failures are non-fatal (local / non-GHA may lack sudo).
	cmds := [][]string{
		{"bash", "-c", "sudo rm -rf /usr/share/dotnet /usr/local/lib/android /opt/ghc /opt/hostedtoolcache/CodeQL /usr/local/share/boost 2>/dev/null || true"},
		{"bash", "-c", "sudo docker system prune -af 2>/dev/null || true"},
		{"df", "-h"},
	}
	for _, c := range cmds {
		if err := deps.Runner.Run(ctx, c[0], c[1:]...); err != nil {
			deps.Logf("  (ignored) %v: %v", c, err)
		}
	}
	return nil
}

func prepWindowsHost(ctx context.Context, deps foundation.Deps) error {
	// Bootstrap tools for the builder only. Consumer kit is still cl/mingw-free via xwin.
	deps.Logf("PrepHost(windows): ensuring ninja + cmake for bootstrap build...")
	// choco is on GHA windows-latest; ignore if missing locally.
	if err := deps.Runner.Run(ctx, "choco", "install", "ninja", "cmake", "--version=3.31.6", "-y", "--no-progress"); err != nil {
		deps.Logf("  choco install: %v (continuing if tools already on PATH)", err)
	}
	if out, err := deps.Runner.Output(ctx, "cmake", "--version"); err != nil {
		return fmt.Errorf("cmake required on windows host: %w", err)
	} else {
		deps.Logf("  %s", strings.Split(strings.TrimSpace(out), "\n")[0])
	}
	if out, err := deps.Runner.Output(ctx, "ninja", "--version"); err != nil {
		return fmt.Errorf("ninja required on windows host: %w", err)
	} else {
		deps.Logf("  ninja %s", strings.TrimSpace(out))
	}
	return nil
}
