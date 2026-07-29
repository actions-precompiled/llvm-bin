// Command apc builds the actions-precompiled LLVM distribution.
//
//	go run . list
//	go run . build llvmorg-21.1.0
//	go run . generate workflow --force
//
// Linux builds mount this binary into Docker and run: /apc work
package main

import (
	"context"
	"runtime"

	"github.com/actions-precompiled/foundation"
)

func main() {
	foundation.Main(llvmPackage{})
}

type llvmPackage struct{}

func (llvmPackage) Meta() foundation.Meta {
	return foundation.Meta{
		Name:            "llvm",
		UpstreamRepoAPI: "llvm/llvm-project",
		UpstreamGit:     "https://github.com/llvm/llvm-project.git",
		ImageName:       "llvm-buildenv",
		Binary:          "clang",
		VersionEnv:      "LLVM_VERSION",
		Description:     "Relocatable LLVM/Clang kitchen-sink toolchain (tools + runtimes). Tech demo: tagged llvmorg-* releases (and explicit tags only).",
		Homepage:        "https://github.com/llvm/llvm-project",
		DefaultTargets: []string{
			foundation.TargetLinuxAMD64,
			foundation.TargetLinuxAArch64,
			"windows-amd64",
		},
	}
}

func (p llvmPackage) Work(ctx context.Context, deps foundation.Deps, req foundation.BuildRequest) error {
	if foundation.IsWindowsTarget(req.Target) {
		return workWindows(ctx, deps, p.Meta().Normalize(), req)
	}
	return workLinux(ctx, deps, p.Meta().Normalize(), req)
}

func (p llvmPackage) Smoke(ctx context.Context, deps foundation.Deps, req foundation.SmokeRequest) error {
	meta := p.Meta().Normalize()
	if foundation.IsWindowsTarget(req.Target) {
		return smokeWindows(ctx, deps, meta, req)
	}
	return smokeLinux(ctx, deps, meta, req)
}

func defaultHostTarget() string {
	switch runtime.GOOS {
	case "windows":
		if runtime.GOARCH == "arm64" {
			return "windows-arm64"
		}
		return "windows-amd64"
	default:
		return foundation.HostTarget()
	}
}
