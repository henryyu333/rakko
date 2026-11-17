package aiusage

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
)

// claudeBinary finds Claude Code: an explicit path, then PATH, then the
// locations a user's shell installs into. A Finder-launched app has a
// minimal PATH, so the install locations are checked by name; each platform
// lists the names and directories its installer writes.
func claudeBinary(env Env) (string, error) {
	if env.ClaudePath != "" {
		return env.ClaudePath, nil
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	for _, p := range claudeCandidates(env) {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && executableFile(fi) {
			return p, nil
		}
	}
	return "", errClaudeNotInstalled
}

// cleanClaudeEnv strips the environment variables that would point Claude
// Code at another endpoint or token, and lets it print without telemetry
// or self-updating. CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC is left only
// when the user set it: with it, /usage skips its request and prints a
// stale reading.
func cleanClaudeEnv(env []string) []string {
	blocked := map[string]bool{
		"ANTHROPIC_BASE_URL":           true,
		"ANTHROPIC_AUTH_TOKEN":         true,
		"ANTHROPIC_API_KEY":            true,
		"CLAUDE_CODE_OAUTH_TOKEN":      true,
		"CLAUDE_CODE_ENTRYPOINT":       true,
		"CLAUDECODE":                   true,
		"CLAUDE_CODE_SSE_PORT":         true,
		"ANTHROPIC_BETAS":              true,
		"CLAUDE_CODE_EXTRA_BODY":       true,
		"CLAUDE_CODE_PROMPT_CACHE_TTL": true,
		"DISABLE_TELEMETRY":            true,
		"DISABLE_ERROR_REPORTING":      true,
		"DISABLE_AUTOUPDATER":          true,
	}
	out := make([]string, 0, len(env)+3)
	for _, e := range env {
		k, _, _ := strings.Cut(e, "=")
		if !blocked[k] {
			out = append(out, e)
		}
	}
	return append(out,
		"DISABLE_TELEMETRY=1",
		"DISABLE_ERROR_REPORTING=1",
		"DISABLE_AUTOUPDATER=1",
	)
}

// runClaudeCommand runs the real Claude Code in a throwaway directory with
// an empty stdin and its own session, so cancelling the context ends every
// process the run started. Claude Code (~100 MB) is not something to wait
// on the main thread for.
func runClaudeCommand(ctx context.Context, env Env, args []string, configDir string) (string, string, error) {
	path, err := claudeBinary(env)
	if err != nil {
		return "", "", err
	}
	dir, err := os.MkdirTemp("", "rakko-claude-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(dir)

	childEnv := cleanClaudeEnv(os.Environ())
	if configDir != "" {
		childEnv = setEnv(childEnv, "CLAUDE_CONFIG_DIR", configDir)
	}
	// The builder owns the child's environment: a batch launcher has to add
	// the entries its command line reads.
	cmd, err := claudeCommand(path, args, childEnv)
	if err != nil {
		return "", "", err
	}
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader("")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	group, err := startInGroup(cmd)
	if err != nil {
		return "", "", err
	}
	defer group.release()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return out.String(), stderr.String(), err
	case <-ctx.Done():
		group.kill()
		<-done
		return out.String(), stderr.String(), ctx.Err()
	}
}

// setEnv replaces (or appends) one variable in an environment list.
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
