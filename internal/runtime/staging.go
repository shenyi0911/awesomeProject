package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"awesomeProject/internal/domain/manifest"
	"awesomeProject/pkg/logger"
)

// orphanCleanupDelay gives stopped orphan processes time to release the staging directory.
const orphanCleanupDelay = 500 * time.Millisecond

// ensureStaged makes sure the instance is bound to a manifest and a local staging directory (idempotent).
func (m *Manager) ensureStaged(ctx context.Context, inst *AlgoInstance) error {
	if m.opts.Resolver == nil {
		return fmt.Errorf("VersionResolver is not configured; cannot fetch algorithm files from object storage")
	}
	if m.opts.Downloader == nil {
		return fmt.Errorf("BlobDownloader is not configured; cannot download algorithm files")
	}

	inst.mu.Lock()
	if inst.manifest != nil && inst.versionID != "" && inst.dir != "" && dirExists(inst.stagingDir) {
		inst.mu.Unlock()
		return nil
	}
	appPort := inst.appPort
	inst.mu.Unlock()

	versionID, ver, status, err := m.opts.Resolver.LatestReleasedVersion(ctx, inst.Code)
	if err != nil {
		return fmt.Errorf("resolve released version failed: %w", err)
	}
	if status != statusReleased || versionID == "" {
		return fmt.Errorf("no released version (latest status=%s), cannot launch", status)
	}

	staging, err := m.stagingDir(inst.Code, ver)
	if err != nil {
		return err
	}
	var downloaded []string
	if markerMatches(staging, versionID) && dirExists(staging) {
		downloaded, err = collectRelPaths(staging)
		if err != nil {
			return fmt.Errorf("list staging directory failed: %w", err)
		}
		logger.Info("reusing staged algorithm", "code", inst.Code, "version", ver, "files", len(downloaded))
	} else {
		if err := m.prepareStaging(inst.Code, staging); err != nil {
			return fmt.Errorf("prepare staging directory failed: %w", err)
		}
		files, ferr := m.opts.Resolver.VersionFiles(ctx, versionID)
		if ferr != nil {
			return fmt.Errorf("list version files failed: %w", ferr)
		}
		if len(files) == 0 {
			return fmt.Errorf("released version has no files")
		}
		downloaded = make([]string, 0, len(files))
		for _, f := range files {
			rc, derr := m.opts.Downloader.Download(ctx, f.Key)
			if derr != nil {
				return fmt.Errorf("download file %s failed: %w", f.Key, derr)
			}
			rel, rerr := sanitizeRel(f.RelPath)
			if rerr != nil {
				rc.Close()
				return rerr
			}
			dest := filepath.Join(staging, rel)
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				rc.Close()
				return fmt.Errorf("create subdirectory failed: %w", err)
			}
			out, werr := os.Create(dest)
			if werr != nil {
				rc.Close()
				return fmt.Errorf("create file %s failed: %w", dest, werr)
			}
			if _, cerr := io.Copy(out, rc); cerr != nil {
				out.Close()
				rc.Close()
				return fmt.Errorf("write %s failed: %w", dest, cerr)
			}
			out.Close()
			rc.Close()
			downloaded = append(downloaded, rel)
		}
		if err := writeMarker(staging, versionID); err != nil {
			return fmt.Errorf("write staging marker failed: %w", err)
		}
	}

	manifestRel, merr := locateManifest(downloaded)
	if merr != nil {
		return merr
	}
	if manifestRel == "" {
		return fmt.Errorf("released version is missing algorithm.yaml (cannot resolve launch contract)")
	}
	data, rerr := os.ReadFile(filepath.Join(staging, manifestRel))
	if rerr != nil {
		return fmt.Errorf("read algorithm.yaml failed: %w", rerr)
	}
	mfst, perr := manifest.Parse(data)
	if perr != nil {
		return fmt.Errorf("parse algorithm.yaml failed: %w", perr)
	}
	// The app ID and routing key come from the platform code.
	mfst.Code = inst.Code

	if appPort <= 0 {
		appPort = m.reservePort()
		if appPort <= 0 {
			return fmt.Errorf("algorithm %s cannot allocate a free listen port", inst.Code)
		}
		if m.opts.PortResolver != nil {
			if err := m.opts.PortResolver.SetPort(ctx, inst.Code, appPort); err != nil {
				logger.Warn("persist algorithm port failed", "code", inst.Code, "port", appPort, "error", err)
			}
		}
	}
	if mfst.Port > 0 && mfst.Port != appPort {
		logger.Warn("manifest.port differs from the platform-assigned port; using the platform port", "code", inst.Code, "manifest-port", mfst.Port, "platform-port", appPort)
	}

	runDir := filepath.Join(staging, filepath.FromSlash(filepath.Dir(manifestRel)))
	if err := m.installDeps(ctx, inst.Code, runDir, mfst); err != nil {
		return fmt.Errorf("algorithm %s dependency installation failed: %w", inst.Code, err)
	}

	inst.mu.Lock()
	inst.manifest = mfst
	inst.version = ver
	inst.versionID = versionID
	inst.stagingDir = staging
	inst.dir = runDir
	inst.appPort = appPort
	inst.mu.Unlock()

	logger.Info("algorithm staged", "code", inst.Code, "version", ver, "dir", staging,
		"files", len(downloaded), "manifest", manifestRel, "app-port", appPort)
	return nil
}

// installDeps prepares an isolated runtime environment for algorithms that declare Python dependencies.
// A venv is created only when the entry interpreter is Python; pip install is skipped when the
// dependency signature is unchanged.
func (m *Manager) installDeps(ctx context.Context, code, dir string, mfst *manifest.Manifest) error {
	deps := append([]string{}, mfst.Dependencies...)
	if data, err := os.ReadFile(filepath.Join(dir, "requirements.txt")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if i := strings.Index(line, "#"); i >= 0 {
				line = strings.TrimSpace(line[:i])
			}
			if line != "" {
				deps = append(deps, line)
			}
		}
	}
	if len(deps) == 0 {
		return nil
	}

	fields := strings.Fields(mfst.Entry)
	if len(fields) == 0 {
		return nil
	}
	if !strings.Contains(strings.ToLower(fields[0]), "python") {
		// A non-Python algorithm declared pip dependencies: installing is pointless and rewriting entry would break launch.
		logger.Warn("algorithm declares dependencies but entry is not Python; skipping venv installation", "code", code, "entry", mfst.Entry)
		return nil
	}

	seen := map[string]bool{}
	uniq := deps[:0]
	for _, d := range deps {
		if !seen[d] {
			seen[d] = true
			uniq = append(uniq, d)
		}
	}
	deps = uniq

	venvPy := venvPythonRel()
	venvDir := filepath.Join(dir, venvPythonDirName())
	sig := depsSignature(deps)
	sigFile := filepath.Join(venvDir, ".deps-signature")

	if _, err := os.Stat(filepath.Join(venvDir, "pyvenv.cfg")); err != nil {
		logger.Info("creating venv", "code", code, "dir", venvDir)
		if out, err := runCmdEnv(ctx, dir, pythonBin(), nil, "-m", "venv", venvPythonDirName()); err != nil {
			return fmt.Errorf("create venv failed: %w\n%s", err, out)
		}
	} else if data, err := os.ReadFile(sigFile); err == nil && strings.TrimSpace(string(data)) == sig {
		// Dependencies unchanged: skip reinstalling.
		mfst.Entry = rewriteEntryPython(mfst.Entry, venvPy)
		return nil
	}

	var installEnv []string
	pipArgs := []string{"-m", "pip", "install"}
	if dirExists(filepath.Join(dir, "wheels")) {
		pipArgs = append(pipArgs, "--no-index", "--find-links", "./wheels")
	} else {
		installEnv = append(installEnv, "PIP_INDEX_URL="+pipIndexURL())
	}
	pipArgs = append(pipArgs, deps...)
	logger.Info("installing algorithm dependencies", "code", code, "deps", deps)
	if out, err := runCmdEnv(ctx, dir, venvPy, installEnv, pipArgs...); err != nil {
		return fmt.Errorf("install dependencies failed: %w\n%s", err, out)
	}
	_ = os.WriteFile(sigFile, []byte(sig), 0o644)

	mfst.Entry = rewriteEntryPython(mfst.Entry, venvPy)
	logger.Info("entry now points to the venv interpreter", "code", code, "entry", mfst.Entry)
	return nil
}

func depsSignature(deps []string) string {
	sum := sha256.Sum256([]byte(strings.Join(deps, "\n")))
	return hex.EncodeToString(sum[:])
}

// venvPythonRel returns the interpreter path inside the venv, relative to the algorithm run directory.
func venvPythonRel() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvPythonDirName(), "Scripts", "python.exe")
	}
	return filepath.Join(venvPythonDirName(), "bin", "python")
}

func venvPythonDirName() string { return ".venv" }

func pythonBin() string {
	if v := os.Getenv("PYTHON_BIN"); v != "" {
		return v
	}
	return "python"
}

func pipIndexURL() string {
	if v := os.Getenv("PIP_INDEX_URL"); v != "" {
		return v
	}
	return "https://pypi.tuna.tsinghua.edu.cn/simple"
}

// rewriteEntryPython replaces the Python interpreter in entry with the venv interpreter.
func rewriteEntryPython(entry, venvPy string) string {
	fields := strings.Fields(entry)
	if len(fields) == 0 {
		return entry
	}
	if strings.Contains(strings.ToLower(fields[0]), "python") {
		fields[0] = venvPy
	} else {
		fields = append([]string{venvPy}, fields...)
	}
	return strings.Join(fields, " ")
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func markerFile(staging string) string {
	return filepath.Join(staging, ".staged")
}

func writeMarker(staging, versionID string) error {
	return os.WriteFile(markerFile(staging), []byte(versionID), 0o644)
}

func markerMatches(staging, versionID string) bool {
	data, err := os.ReadFile(markerFile(staging))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == versionID
}

func collectRelPaths(root string) ([]string, error) {
	var rels []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		rels = append(rels, filepath.ToSlash(rel))
		return nil
	})
	return rels, err
}

func runCmdEnv(ctx context.Context, dir, exe string, extraEnv []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = dir
	// Dependency installation must not see platform secrets either.
	cmd.Env = sanitizedEnv()
	if len(extraEnv) > 0 {
		cmd.Env = append(cmd.Env, extraEnv...)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// locateManifest finds algorithm.yaml among the downloaded files (root first, then shallowest depth;
// multiple manifests at the same depth are considered ambiguous).
func locateManifest(rels []string) (string, error) {
	best := ""
	bestDepth := -1
	count := 0
	for _, rel := range rels {
		p := strings.ToLower(filepath.ToSlash(rel))
		if p != "algorithm.yaml" && !strings.HasSuffix(p, "/algorithm.yaml") {
			continue
		}
		depth := strings.Count(strings.TrimSuffix(p, "algorithm.yaml"), "/")
		switch {
		case best == "" || depth < bestDepth:
			best, bestDepth, count = rel, depth, 1
		case depth == bestDepth:
			count++
		}
	}
	if count > 1 {
		return "", fmt.Errorf("multiple algorithm.yaml files at the same depth; cannot determine the launch contract")
	}
	return best, nil
}

// prepareStaging prepares the staging directory; if clearing fails, it removes leftover processes
// matching the app ID or staging path and retries.
func (m *Manager) prepareStaging(code, staging string) error {
	if _, err := os.Stat(staging); os.IsNotExist(err) {
		return os.MkdirAll(staging, 0o755)
	}
	if err := os.RemoveAll(staging); err == nil {
		return os.MkdirAll(staging, 0o755)
	}
	logger.Warn("failed to remove staging directory (possibly held by an old process); trying to clean up orphan processes", "code", code, "dir", staging)
	if runtime.GOOS == "windows" {
		killOrphanProcesses(code, staging)
		time.Sleep(orphanCleanupDelay)
	}
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("remove staging directory failed (possibly held by a process at %s): %w", staging, err)
	}
	return os.MkdirAll(staging, 0o755)
}

// killOrphanProcesses (Windows only) stops processes whose command line matches the app ID or staging path.
// It never kills all dotnet/daprd processes on the machine, avoiding impact on other services.
func killOrphanProcesses(appID, staging string) {
	esc := func(s string) string { return strings.ReplaceAll(s, "'", "''") }
	ps := fmt.Sprintf(
		"Get-CimInstance Win32_Process -Filter \"Name='dapr.exe' OR Name='daprd.exe' OR Name='dotnet.exe'\" | "+
			"Where-Object { $_.CommandLine -like '*-app-id %s*' -or $_.CommandLine -like '*%s*' } | "+
			"ForEach-Object { Stop-Process -Id $_.ProcessId -Force }",
		esc(appID), esc(staging),
	)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command", ps)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		logger.Warn("clean up orphan processes failed", "app-id", appID, "error", err)
	}
}

// stagingDir returns the staging directory for code/version, rejecting values that escape the
// workspace (defense in depth: versions are also validated at creation time).
func (m *Manager) stagingDir(code, ver string) (string, error) {
	base := filepath.Join(m.opts.Workspace, code)
	dir := filepath.Join(base, ver)
	rel, err := filepath.Rel(base, dir)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid version %q: staging path escapes the workspace", ver)
	}
	return dir, nil
}

// cleanupOldStaging removes sibling version directories after a successful launch, preventing
// staged files of previous versions from accumulating on disk.
func (m *Manager) cleanupOldStaging(code, current string) {
	base := filepath.Join(m.opts.Workspace, code)
	entries, err := os.ReadDir(base)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warn("list staging root failed", "code", code, "error", err)
		}
		return
	}
	current = filepath.Clean(current)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(base, e.Name())
		if filepath.Clean(dir) == current {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			logger.Warn("remove old staging directory failed", "dir", dir, "error", err)
			continue
		}
		logger.Info("old staging directory removed", "dir", dir)
	}
}

// removeStagingRoot removes every staged version of an algorithm (used when the algorithm is deleted).
func (m *Manager) removeStagingRoot(code string) {
	dir := filepath.Join(m.opts.Workspace, code)
	if err := os.RemoveAll(dir); err != nil {
		logger.Warn("remove staging root failed", "dir", dir, "error", err)
	}
}

// sanitizeRel turns an object storage relative path into a safe local relative path without traversal.
func sanitizeRel(p string) (string, error) {
	p = filepath.ToSlash(p)
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("absolute path is not allowed: %q", p)
	}
	segs := strings.Split(p, "/")
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		if s == "" || s == "." {
			continue
		}
		if s == ".." || strings.Contains(s, ":") || strings.ContainsRune(s, 0) {
			return "", fmt.Errorf("invalid file path segment %q in %q", s, p)
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return "", fmt.Errorf("invalid relative file path: %q", p)
	}
	return strings.Join(out, "/"), nil
}
