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
	// Best-effort and non-blocking: rm/prune can take minutes; don't hold up
	// image build / work. Log lands on the runner for debugging.
	const logPath = "/tmp/apc-prephost-clean.log"
	deps.Logf("PrepHost(linux): starting background disk free → %s", logPath)
	// Subshell + & returns immediately; failures ignored inside the job.
	script := `
set +e
{
  echo "apc prephost clean start $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  sudo rm -rf \
    /usr/share/dotnet \
    /usr/local/lib/android \
    /opt/ghc \
    /opt/hostedtoolcache/CodeQL \
    /usr/local/share/boost \
    2>/dev/null
  sudo docker system prune -af 2>/dev/null
  df -h
  echo "apc prephost clean done $(date -u +%Y-%m-%dT%H:%M:%SZ)"
} >` + logPath + ` 2>&1 &
echo "background_pid=$!"
`
	if out, err := deps.Runner.Output(ctx, "bash", "-c", script); err != nil {
		deps.Logf("  (ignored) background clean spawn: %v", err)
	} else {
		deps.Logf("  %s", strings.TrimSpace(out))
	}
	return nil
}

func prepWindowsHost(ctx context.Context, deps foundation.Deps) error {
	// Bootstrap tools for the builder only. Consumer kit is still cl/mingw-free via xwin.
	// Must stay synchronous: cmake/ninja need to exist before Work.
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
