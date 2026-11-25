package aiusage

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DetectQuotaSource reports where Rakko finds credentials for tool without
// making network requests.
func DetectQuotaSource(tool Tool, env Env) QuotaSource {
	switch tool {
	case ToolCodex:
		if auth, err := readCodexAuth(env.Home); err == nil && auth.Tokens.AccessToken != "" {
			return QuotaSource{Kind: SourceLocalLogin}
		}
		return QuotaSource{Kind: SourceNone}
	case ToolClaude:
		if hasClaudeLocalLogin(env) {
			return QuotaSource{Kind: SourceLocalLogin}
		}
		return QuotaSource{Kind: SourceNone}
	case ToolGLM:
		if tok, _, ok := readGLMKey(env); ok && tok != "" {
			return QuotaSource{Kind: SourceLocalKey}
		}
		if env.Secrets != nil && strings.TrimSpace(env.Secrets(ToolGLM)) != "" {
			return QuotaSource{Kind: SourceSavedKey}
		}
		return QuotaSource{Kind: SourceNone}
	case ToolKimi:
		if tok, _, ok := readKimiKey(env); ok && tok != "" {
			return QuotaSource{Kind: SourceLocalKey}
		}
		if env.Secrets != nil && strings.TrimSpace(env.Secrets(ToolKimi)) != "" {
			return QuotaSource{Kind: SourceSavedKey}
		}
		return QuotaSource{Kind: SourceNone}
	case ToolOpencode:
		if tok, ok := readOpenCodeGoKey(env); ok && tok != "" {
			return QuotaSource{Kind: SourceLocalKey}
		}
		if env.Secrets != nil && strings.TrimSpace(env.Secrets(ToolOpencode)) != "" {
			return QuotaSource{Kind: SourceSavedKey}
		}
		return QuotaSource{Kind: SourceNone}
	case ToolCommandCode:
		if tok, ok := readCommandCodeKey(env); ok && tok != "" {
			return QuotaSource{Kind: SourceLocalKey}
		}
		if env.Secrets != nil && strings.TrimSpace(env.Secrets(ToolCommandCode)) != "" {
			return QuotaSource{Kind: SourceSavedKey}
		}
		return QuotaSource{Kind: SourceNone}
	case ToolDeepSeek:
		if tok, ok := readDeepSeekKey(env); ok && tok != "" {
			return QuotaSource{Kind: SourceLocalKey}
		}
		if env.Secrets != nil && strings.TrimSpace(env.Secrets(ToolDeepSeek)) != "" {
			return QuotaSource{Kind: SourceSavedKey}
		}
		return QuotaSource{Kind: SourceNone}
	case ToolCursor:
		if tok, _, ok := readCursorCreds(context.Background(), env); ok && tok != "" {
			return QuotaSource{Kind: SourceLocalLogin}
		}
		if env.Secrets != nil && strings.TrimSpace(env.Secrets(ToolCursor)) != "" {
			return QuotaSource{Kind: SourceSavedKey}
		}
		return QuotaSource{Kind: SourceNone}
	default:
		return QuotaSource{Kind: SourceNone}
	}
}

func hasClaudeLocalLogin(env Env) bool {
	// Check ~/.claude/.credentials.json or macOS Keychain via existing Claude helpers.
	dir := env.ClaudeConfigDir
	if dir == "" {
		dir = joinHome(env.Home, ".claude")
	}
	if data, err := readFile(filepath.Join(dir, ".credentials.json")); err == nil && len(data) > 0 {
		return true
	}
	if env.RakkoClaudeDir != "" {
		if data, err := readFile(filepath.Join(env.RakkoClaudeDir, ".credentials.json")); err == nil && len(data) > 0 {
			return true
		}
	}
	if env.Keychain != nil && runtime.GOOS == "darwin" {
		acct := claudeKeychainAccount(env)
		if out, err := env.Keychain(context.Background(), claudeKeychainServiceFor(env.ClaudeConfigDir), acct); err == nil && len(out) > 0 {
			return true
		}
		if env.RakkoClaudeDir != "" {
			if out, err := env.Keychain(context.Background(), claudeKeychainServiceFor(env.RakkoClaudeDir), acct); err == nil && len(out) > 0 {
				return true
			}
		}
	}
	return false
}

func readClaudeSettingsEnv(env Env) (token, baseURL string) {
	dir := env.ClaudeConfigDir
	if dir == "" {
		dir = joinHome(env.Home, ".claude")
	}
	data, err := readFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return "", ""
	}
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if json.Unmarshal(data, &doc) != nil || doc.Env == nil {
		return "", ""
	}
	return strings.TrimSpace(doc.Env["ANTHROPIC_AUTH_TOKEN"]), strings.TrimSpace(doc.Env["ANTHROPIC_BASE_URL"])
}

func openCodeAuthPaths(home string) []string {
	var paths []string
	if home != "" {
		paths = append(paths, filepath.Join(home, ".local", "share", "opencode", "auth.json"))
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "opencode", "auth.json"))
	}
	if runtime.GOOS == "windows" {
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			paths = append(paths, filepath.Join(local, "opencode", "auth.json"))
		}
		if app := os.Getenv("APPDATA"); app != "" {
			paths = append(paths, filepath.Join(app, "opencode", "auth.json"))
		}
	}
	return paths
}

func readOpenCodeAuthEntry(env Env, providers ...string) string {
	for _, p := range openCodeAuthPaths(env.Home) {
		data, err := readFile(p)
		if err != nil {
			continue
		}
		var doc map[string]struct {
			Type string `json:"type"`
			Key  string `json:"key"`
		}
		if json.Unmarshal(data, &doc) != nil {
			continue
		}
		for _, name := range providers {
			if e, ok := doc[name]; ok && strings.TrimSpace(e.Key) != "" {
				return strings.TrimSpace(e.Key)
			}
		}
	}
	return ""
}

// hostAllowed reports whether a base URL's host is one of domains or a
// subdomain of one: a token configured against some other URL — a proxy
// or a look-alike whose name merely mentions the vendor — must never be
// claimed and sent to the vendor's real endpoint.
func hostAllowed(raw string, domains ...string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, d := range domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

func readGLMKey(env Env) (key, base string, ok bool) {
	tok, baseURL := readClaudeSettingsEnv(env)
	if tok != "" && hostAllowed(baseURL, "bigmodel.cn", "z.ai") {
		if hostAllowed(baseURL, "z.ai") {
			return tok, "https://api.z.ai", true
		}
		return tok, "https://open.bigmodel.cn", true
	}
	if k := readOpenCodeAuthEntry(env, "zhipuai-coding-plan", "zhipuai", "glm"); k != "" {
		return k, "https://open.bigmodel.cn", true
	}
	if k := readOpenCodeAuthEntry(env, "zai-coding-plan", "zai"); k != "" {
		return k, "https://api.z.ai", true
	}
	return "", "", false
}

func readKimiKey(env Env) (key, base string, ok bool) {
	tok, baseURL := readClaudeSettingsEnv(env)
	if tok != "" && hostAllowed(baseURL, "api.kimi.com", "api.kimi.ai") {
		return tok, strings.TrimRight(baseURL, "/"), true
	}
	if k := readOpenCodeAuthEntry(env, "kimi-for-coding", "kimi-coding", "kimi"); k != "" {
		return k, "https://api.kimi.com/coding/v1", true
	}
	return "", "", false
}

func readOpenCodeGoKey(env Env) (string, bool) {
	if k := readOpenCodeAuthEntry(env, "opencode-go", "opencode"); k != "" {
		return k, true
	}
	return "", false
}

func readCommandCodeKey(env Env) (string, bool) {
	data, err := readFile(joinHome(env.Home, ".commandcode", "auth.json"))
	if err != nil {
		return "", false
	}
	var a struct {
		APIKey string `json:"apiKey"`
	}
	if json.Unmarshal(data, &a) != nil || strings.TrimSpace(a.APIKey) == "" {
		return "", false
	}
	return strings.TrimSpace(a.APIKey), true
}

func readDeepSeekKey(env Env) (string, bool) {
	tok, baseURL := readClaudeSettingsEnv(env)
	if tok != "" && hostAllowed(baseURL, "deepseek.com") {
		return tok, true
	}
	if k := readOpenCodeAuthEntry(env, "deepseek"); k != "" {
		return k, true
	}
	return "", false
}

func cursorAuthPaths(home string) []string {
	var paths []string
	if home != "" {
		paths = append(paths, filepath.Join(home, ".cursor", "auth.json"))
	}
	if runtime.GOOS == "windows" {
		if app := os.Getenv("APPDATA"); app != "" {
			paths = append(paths, filepath.Join(app, "Cursor", "auth.json"))
		}
	}
	return paths
}

func cursorStateDBPaths(home string) []string {
	var paths []string
	if runtime.GOOS == "darwin" && home != "" {
		paths = append(paths, filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb"))
	} else if runtime.GOOS == "windows" {
		if app := os.Getenv("APPDATA"); app != "" {
			paths = append(paths, filepath.Join(app, "Cursor", "User", "globalStorage", "state.vscdb"))
		}
		if home != "" {
			paths = append(paths, filepath.Join(home, "AppData", "Roaming", "Cursor", "User", "globalStorage", "state.vscdb"))
		}
	} else if home != "" {
		paths = append(paths, filepath.Join(home, ".config", "Cursor", "User", "globalStorage", "state.vscdb"))
	}
	return paths
}

func readCursorCreds(ctx context.Context, env Env) (token, plan string, ok bool) {
	// 1. Check ~/.cursor/auth.json
	for _, p := range cursorAuthPaths(env.Home) {
		data, err := readFile(p)
		if err != nil {
			continue
		}
		var a struct {
			AccessToken string `json:"accessToken"`
		}
		if json.Unmarshal(data, &a) == nil && strings.TrimSpace(a.AccessToken) != "" {
			return strings.TrimSpace(a.AccessToken), "", true
		}
	}
	// 2. Check Cursor IDE state.vscdb directly (read-only, never copying the 3GB+ DB)
	for _, db := range cursorStateDBPaths(env.Home) {
		if _, err := os.Stat(db); err != nil {
			continue
		}
		raw, err := querySQLiteJSONReadOnly(ctx, "cursor", db,
			`SELECT key, value FROM ItemTable WHERE key IN ('cursorAuth/accessToken', 'cursorAuth/stripeMembershipType')`)
		if err != nil || len(raw) == 0 {
			continue
		}
		var rows []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if json.Unmarshal(raw, &rows) != nil {
			continue
		}
		var tok, membership string
		for _, r := range rows {
			switch r.Key {
			case "cursorAuth/accessToken":
				tok = strings.TrimSpace(r.Value)
			case "cursorAuth/stripeMembershipType":
				membership = strings.TrimSpace(r.Value)
			}
		}
		if tok != "" {
			return tok, prettyPlan(membership), true
		}
	}
	// 3. Check macOS Keychain cursor-access-token
	if env.Keychain != nil && runtime.GOOS == "darwin" {
		if out, err := env.Keychain(ctx, "cursor-access-token", "cursor-user"); err == nil && len(out) > 0 {
			return strings.TrimSpace(string(out)), "", true
		}
	}
	return "", "", false
}
