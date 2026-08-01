package main

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	"github.com/actions-precompiled/foundation"
)

// Host-side preclone: PrepHost starts clones in goroutines; callers Wait() via
// WaitGroup before build/docker so install || clone, then build.

type precloneResult struct {
	Src      string
	Ref      string
	Artifact string
	SHA      string
	Err      error
}

type precloneEntry struct {
	wg  sync.WaitGroup
	res precloneResult
}

var preclones sync.Map // version → *precloneEntry

func precloneCacheDir(workDir, version string) string {
	return filepath.Join(workDir, ".cache", "src", foundation.SafePathComponent(version))
}

// startPreclones kicks off one host git clone per version (idempotent).
func startPreclones(ctx context.Context, deps foundation.Deps, meta foundation.Meta, versions []string) {
	if deps.WorkDir == "" || len(versions) == 0 {
		return
	}
	meta = meta.Normalize()
	for _, v := range versions {
		v := strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, loaded := preclones.Load(v); loaded {
			continue
		}
		e := &precloneEntry{}
		if _, loaded := preclones.LoadOrStore(v, e); loaded {
			continue
		}
		e.wg.Add(1)
		src := precloneCacheDir(deps.WorkDir, v)
		deps.Logf("preclone: starting %s → %s", v, src)
		go func(version, src string, e *precloneEntry) {
			defer e.wg.Done()
			ref, art, sha, err := cloneUpstream(ctx, deps, meta.UpstreamGit, version, src)
			if err != nil {
				deps.Logf("preclone: %s failed: %v", version, err)
				e.res = precloneResult{Err: err}
				return
			}
			deps.Logf("preclone: %s ready ref=%s sha=%s", version, ref, sha)
			e.res = precloneResult{Src: src, Ref: ref, Artifact: art, SHA: sha}
		}(v, src, e)
	}
}

// WaitPrefetch blocks until PrepHost preclone for version finishes (if any).
// Implements optional foundation hook so the host waits before docker mount.
func (llvmPackage) WaitPrefetch(ctx context.Context, version string) error {
	_ = ctx
	e, ok := loadPreclone(version)
	if !ok {
		return nil
	}
	e.wg.Wait()
	return e.res.Err
}

func loadPreclone(version string) (*precloneEntry, bool) {
	v, ok := preclones.Load(version)
	if !ok {
		return nil, false
	}
	return v.(*precloneEntry), true
}

// resolveSource waits for PrepHost preclone when present; otherwise clones into fallbackSrc.
func resolveSource(ctx context.Context, deps foundation.Deps, meta foundation.Meta, version, fallbackSrc string) (src, ref, artifact, sha string, err error) {
	// Container: host preclone already waited on host; mounted at APC_PREBUILT_SRC.
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

	if e, ok := loadPreclone(version); ok {
		deps.Logf("source: waiting for preclone %s", version)
		e.wg.Wait()
		if e.res.Err != nil {
			return "", "", "", "", e.res.Err
		}
		return e.res.Src, e.res.Ref, e.res.Artifact, e.res.SHA, nil
	}

	ref, artifact, sha, err = cloneUpstream(ctx, deps, meta.UpstreamGit, version, fallbackSrc)
	return fallbackSrc, ref, artifact, sha, err
}

func gitIdentity(ctx context.Context, deps foundation.Deps, src, versionRaw string) (ref, artifact, sha string, err error) {
	out, err := deps.Runner.Output(ctx, "git", "-C", src, "rev-parse", "--short=12", "HEAD")
	if err != nil {
		return "", "", "", err
	}
	sha = strings.TrimSpace(out)
	out, err = deps.Runner.Output(ctx, "git", "-C", src, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		ref = versionRaw
	} else {
		ref = strings.TrimSpace(out)
		if ref == "HEAD" {
			ref = versionRaw
		}
	}
	switch {
	case versionRaw == "trunk" || versionRaw == "main" || strings.HasPrefix(versionRaw, "trunk-"):
		artifact = "trunk-" + sha
	case strings.HasPrefix(versionRaw, "llvmorg-"):
		artifact = versionRaw
	default:
		artifact = foundation.VersionBare(versionRaw)
	}
	return ref, artifact, sha, nil
}
