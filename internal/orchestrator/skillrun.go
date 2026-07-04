package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"samwise/internal/runtime"
)

const (
	skillRunTimeout  = 30 * time.Second
	maxSkillOutput   = 64 << 10 // cap captured stdout/stderr (bytes)
	venvBuildTimeout = 5 * time.Minute
)

// BuildSkillVenv builds a per-skill Python venv from the bundle's
// requirements.txt, so skill_run can later execute it offline. Runs at import
// time (trusted user, networked); a no-op when there's no requirements.txt. The
// venv lives in the bundle dir and is rebuilt fresh on every (re)import. Returns
// an error (with the pip output) the caller can show the importing user.
func (o *Orchestrator) BuildSkillVenv(ctx context.Context, userID int64, skillName string) error {
	bundleDir := o.SkillBundleDir(userID, skillName)
	req := filepath.Join(bundleDir, "requirements.txt")
	if !fileExists(req) {
		return nil
	}
	venv := filepath.Join(bundleDir, ".venv")
	_ = os.RemoveAll(venv)

	ctx, cancel := context.WithTimeout(ctx, venvBuildTimeout)
	defer cancel()
	if out, err := runCmd(ctx, bundleDir, "python3", "-m", "venv", venv); err != nil {
		return fmt.Errorf("creating venv: %s", truncate(out, 400))
	}
	pip := filepath.Join(venv, "bin", "pip")
	if out, err := runCmd(ctx, bundleDir, pip, "install", "--no-input", "-r", req); err != nil {
		_ = os.RemoveAll(venv) // don't leave a half-installed venv
		return fmt.Errorf("pip install failed: %s", truncate(out, 800))
	}
	return nil
}

func runCmd(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// RunSkill executes a runnable skill's entrypoint in a constrained way and
// returns its stdout. This is the SAFE path for letting unregistered group
// members run scripts: a fixed entrypoint (no shell), input passed as a single
// argv argument (no injection), an env limited to the secret names the skill
// declared (nothing else), and a timeout + output cap. When isolation is on it
// runs under the owner's per-user uid (same boundary as agent runs).
//
// readOnly marks an unregistered (group-stranger) caller: those may run a skill
// only if its audience is 'everyone'. A paired caller may run any runnable skill.
func (o *Orchestrator) RunSkill(ctx context.Context, userID int64, skillName, input string, readOnly bool) (string, error) {
	sk, err := o.db.GetSkillByName(ctx, userID, skillName)
	if err != nil || sk == nil {
		return "", fmt.Errorf("no skill named %q", skillName)
	}
	if !sk.Enabled {
		return "", fmt.Errorf("skill %q is disabled", skillName)
	}
	if !sk.Runnable() {
		return "", fmt.Errorf("skill %q has no entrypoint to run", skillName)
	}
	if readOnly && !sk.OpenToEveryone() {
		return "", fmt.Errorf("skill %q is available to registered users only", skillName)
	}

	bundleDir := o.SkillBundleDir(userID, sk.Name)
	entry := filepath.Join(bundleDir, sk.Entrypoint)
	// Refuse a path that escapes the bundle (e.g. "../../etc/passwd").
	if !strings.HasPrefix(filepath.Clean(entry)+string(os.PathSeparator),
		filepath.Clean(bundleDir)+string(os.PathSeparator)) {
		return "", fmt.Errorf("skill %q has an invalid entrypoint", skillName)
	}
	if _, err := os.Stat(entry); err != nil {
		return "", fmt.Errorf("skill %q entrypoint %q not found", skillName, sk.Entrypoint)
	}

	argv := o.skillArgv(bundleDir, entry, input)
	env := o.skillEnv(ctx, userID, bundleDir, sk.EnvKeys)

	runCtx, cancel := context.WithTimeout(ctx, skillRunTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir = bundleDir
	cmd.Env = env
	if o.isolate {
		iso := o.runIsolation(userID)
		// The bundle (esp. a venv built by the import process as root) must be owned
		// by the run uid so the dropped process can read/execute it.
		if err := o.ensureWorkspaceOwner(userID, iso); err != nil {
			return "", fmt.Errorf("preparing skill sandbox: %w", err)
		}
		if err := runtime.ApplyIsolation(cmd, iso); err != nil {
			return "", fmt.Errorf("skill sandbox: %w", err)
		}
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &capWriter{buf: &out, max: maxSkillOutput}
	cmd.Stderr = &capWriter{buf: &errb, max: maxSkillOutput}

	runErr := cmd.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		o.db.AddAuditEvent(ctx, userID, 0, "tool", "skill_run", sk.Name, "timeout")
		return "", fmt.Errorf("skill %q timed out after %s", skillName, skillRunTimeout)
	}
	if runErr != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = runErr.Error()
		}
		o.db.AddAuditEvent(ctx, userID, 0, "tool", "skill_run", sk.Name, "error")
		return "", fmt.Errorf("skill %q failed: %s", skillName, truncate(msg, 600))
	}
	o.db.AddAuditEvent(ctx, userID, 0, "tool", "skill_run", sk.Name, "ok")
	return strings.TrimSpace(out.String()), nil
}

// skillArgv builds the command: a .py entrypoint runs under the skill's own venv
// python if it has one, else the system python3; anything else is exec'd
// directly (it must be an executable). Input is always a single argv argument —
// never interpolated into a shell — so it can't inject commands.
func (o *Orchestrator) skillArgv(bundleDir, entry, input string) []string {
	if strings.HasSuffix(entry, ".py") {
		py := "python3"
		if venvPy := filepath.Join(bundleDir, ".venv", "bin", "python"); fileExists(venvPy) {
			py = venvPy
		}
		return []string{py, entry, input}
	}
	return []string{entry, input}
}

// skillEnv builds a minimal environment plus ONLY the secrets the skill declared
// in env_keys — never the owner's full secret set.
func (o *Orchestrator) skillEnv(ctx context.Context, userID int64, bundleDir, envKeys string) []string {
	env := []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=" + bundleDir,
		"LANG=C.UTF-8",
		"PYTHONUNBUFFERED=1",
	}
	keys := splitCSV(envKeys)
	if len(keys) == 0 {
		return env
	}
	secrets := o.userSecretsEnv(ctx, userID)
	for _, k := range keys {
		if v, ok := secrets[k]; ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// capWriter discards anything beyond max bytes (a runaway script can't fill RAM).
type capWriter struct {
	buf *bytes.Buffer
	max int
}

func (c *capWriter) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil // pretend full write so the child isn't killed by EPIPE
}
