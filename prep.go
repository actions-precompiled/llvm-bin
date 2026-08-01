package main

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/actions-precompiled/foundation"
)

// PrepHost implements foundation.HostPrep. CI only runs `go run`; OS weirdness lives here.
//
// Pattern:
//   - synchronous: install / ensure tools required before Work
//   - asynchronous: best-effort disk free (must not block image/build)
func (llvmPackage) PrepHost(ctx context.Context, deps foundation.Deps, cfg foundation.Config) error {
	// Start host git clones immediately (parallel with tool install + docker image build).
	startPreclones(ctx, deps, llvmPackage{}.Meta(), cfg.Versions)

	switch runtime.GOOS {
	case "windows":
		return prepWindowsHost(ctx, deps)
	default:
		return prepLinuxHost(ctx, deps)
	}
}

func prepLinuxHost(ctx context.Context, deps foundation.Deps) error {
	// Sync: host tools for orchestration (docker image has the real toolchain).
	// Nothing hard-required today; keep the hook for future host deps.
	if err := ensureLinuxHostTools(ctx, deps); err != nil {
		return err
	}

	// Async: free GHA disk while docker build / work proceeds.
	startBackgroundDiskFree(ctx, deps)
	return nil
}

func ensureLinuxHostTools(ctx context.Context, deps foundation.Deps) error {
	// Docker is required for linux-* Work inject; fail fast if missing.
	if _, err := deps.Runner.Output(ctx, "docker", "version"); err != nil {
		return fmt.Errorf("docker required on linux host for package builds: %w", err)
	}
	deps.Logf("PrepHost(linux): docker OK")
	return nil
}

func prepWindowsHost(ctx context.Context, deps foundation.Deps) error {
	// Sync: bootstrap tools must exist before Work (MSVC build is on the host).
	deps.Logf("PrepHost(windows): ensuring ninja + cmake for bootstrap build...")
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

	// Async: nothing critical to purge on windows-latest today; hook reserved.
	return nil
}

// startBackgroundDiskFree kicks off best-effort cleanup and returns immediately.
// Only runs on GHA (or when APC_FREE_DISK is set).
func startBackgroundDiskFree(ctx context.Context, deps foundation.Deps) {
	if deps.Env.Get("GITHUB_ACTIONS") == "" && !foundation.EnvFlag(deps.Env, "APC_FREE_DISK") {
		return
	}
	const logPath = "/tmp/apc-prephost-clean.log"
	deps.Logf("PrepHost: background disk free → %s", logPath)
	// Subshell + & returns at once; failures ignored inside the job.
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
  # Prune unused docker data; may race a concurrent build — best-effort only.
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
}
