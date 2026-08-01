package main

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	"github.com/actions-precompiled/foundation"
)

// Host-side preclone: started during PrepHost (parallel with tool install / image build).
// Work waits on the result, then configures/builds.

type precloneResult struct {
	Src      string
	Ref      string
	Artifact string
	SHA      string
	Err      error
}

var precloneMu sync.Mutex
var precloneWait = map[string]<-chan precloneResult{}

func precloneCacheDir(workDir, version string) string {
	return filepath.Join(workDir, ".cache", "src", foundation.SafePathComponent(version))
}

// startPreclones kicks off one host git clone per version (if not already started).
func startPreclones(ctx context.Context, deps foundation.Deps, meta foundation.Meta, versions []string) {
	if deps.WorkDir == "" || len(versions) == 0 {
		return
	}
	meta = meta.Normalize()
	for _, v := range versions {
		v := v
		if v == "" {
			continue
		}
		precloneMu.Lock()
		if _, ok := precloneWait[v]; ok {
			precloneMu.Unlock()
			continue
		}
		ch := make(chan precloneResult, 1)
		precloneWait[v] = ch
		precloneMu.Unlock()

		src := precloneCacheDir(deps.WorkDir, v)
		deps.Logf("preclone: starting %s → %s", v, src)
		go func() {
			ref, art, sha, err := cloneUpstream(ctx, deps, meta.UpstreamGit, v, src)
			if err != nil {
				deps.Logf("preclone: %s failed: %v", v, err)
				ch <- precloneResult{Err: err}
				return
			}
			deps.Logf("preclone: %s ready ref=%s sha=%s", v, ref, sha)
			ch <- precloneResult{Src: src, Ref: ref, Artifact: art, SHA: sha}
		}()
	}
}

// resolveSource waits for a PrepHost preclone when present; otherwise clones into fallbackSrc.
func resolveSource(ctx context.Context, deps foundation.Deps, meta foundation.Meta, version, fallbackSrc string) (src, ref, artifact, sha string, err error) {
	// Container: host preclone mounted at APC_PREBUILT_SRC.
	if pre := deps.Env.Get("APC_PREBUILT_SRC"); pre != "" {
		if st, e := deps.FS.Stat(pre); e == nil && st.IsDir() {
			ref, artifact, sha, err = gitIdentity(ctx, deps, pre, version)
			if err != nil {
				return "", "", "", "", err
			}
			deps.Logf("source: using prebuilt mount %s", pre)
			return pre, ref, artifact, sha, nil
		}
	}

	precloneMu.Lock()
	ch := precloneWait[version]
	precloneMu.Unlock()
	if ch != nil {
		deps.Logf("source: waiting for preclone %s", version)
		res := <-ch
		// Re-arm a closed wait? single-use channel with buffer 1 — only one waiter.
		// If multiple targets share version, re-store completed result for others.
		precloneMu.Lock()
		done := make(chan precloneResult, 1)
		done <- res
		precloneWait[version] = done
		precloneMu.Unlock()
		if res.Err != nil {
			return "", "", "", "", res.Err
		}
		return res.Src, res.Ref, res.Artifact, res.SHA, nil
	}

	// No preclone — clone now into fallbackSrc.
	ref, artifact, sha, err = cloneUpstream(ctx, deps, meta.UpstreamGit, version, fallbackSrc)
	return fallbackSrc, ref, artifact, sha, err
}

func gitIdentity(ctx context.Context, deps foundation.Deps, src, versionRaw string) (ref, artifact, sha string, err error) {
	out, err := deps.Runner.Output(ctx, "git", "-C", src, "rev-parse", "--short=12", "HEAD")
	if err != nil {
		return "", "", "", err
	}
	sha = trimSpace(out)
	out, err = deps.Runner.Output(ctx, "git", "-C", src, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		ref = versionRaw
	} else {
		ref = trimSpace(out)
		if ref == "HEAD" {
			ref = versionRaw
		}
	}
	if versionRaw == "trunk" || versionRaw == "main" || hasPrefix(versionRaw, "trunk-") {
		artifact = "trunk-" + sha
	} else if hasPrefix(versionRaw, "llvmorg-") {
		artifact = versionRaw
	} else {
		artifact = foundation.VersionBare(versionRaw)
	}
	return ref, artifact, sha, nil
}

func trimSpace(s string) string { return strings.TrimSpace(s) }

func hasPrefix(s, p string) bool { return strings.HasPrefix(s, p) }
