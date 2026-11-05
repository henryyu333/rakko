package main

// Version management checks. mygo.json is the only place a version is
// written: mygo reads it for Info.plist, artifact names, the v<version> tag,
// the release title and the update manifest. These checks keep the three
// in step, and they understand both states of a release:
//
//   - before a release the version is newer than the newest tag, so no tag
//     for it exists yet: that is valid, not an error;
//   - after a release the version equals the newest tag, and that tag must
//     sit on this history.
//
// They run in two modes:
//
//   - CI (go test ./...) runs TestReleaseVersion. Development is normal here:
//     the version may equal the newest tag (just released, or not bumped yet)
//     or be newer than it (bump committed, tag not created yet).
//   - A release runs TestReleaseVersionForRelease, which requires the version
//     to be strictly newer than the newest tag. It runs before the upload and
//     again inside `mygo build`, through the generate directive below, so a
//     missed version bump cannot create a release.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

//go:generate go test -count=1 -run ^TestReleaseVersionForRelease$ .

var releaseVersionRE = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)

type releaseUpdates struct {
	GitHub    string `json:"github"`
	TagPrefix string `json:"tagPrefix"`
	PublicKey string `json:"publicKey"`
}

type releaseConfig struct {
	Name       string          `json:"name"`
	Slug       string          `json:"slug"`
	Identifier string          `json:"identifier"`
	Version    string          `json:"version"`
	Main       string          `json:"main"`
	Updates    *releaseUpdates `json:"updates"`
}

// versionTag is one v<version> tag, with what its commit says about the
// version and whether that commit is on the history of HEAD.
type versionTag struct {
	name     string
	version  string
	commit   string
	config   string
	ancestor bool
}

// configProblems checks the fields a release depends on.
func configProblems(c releaseConfig) []string {
	var problems []string
	if !releaseVersionRE.MatchString(c.Version) {
		problems = append(problems, fmt.Sprintf("mygo.json version %q is not X.Y.Z", c.Version))
	}
	if c.Name == "" || c.Slug == "" || c.Identifier == "" || c.Main == "" {
		problems = append(problems, "mygo.json lacks name, slug, identifier or main")
	}
	if c.Updates == nil {
		return append(problems, "mygo.json has no updates block: the app could not check for updates")
	}
	if c.Updates.GitHub == "" {
		problems = append(problems, "mygo.json updates.github is empty")
	}
	if c.Updates.PublicKey == "" {
		problems = append(problems, "mygo.json updates.publicKey is empty: updates would not verify")
	}
	return problems
}

// tagProblems checks the version against the tags that exist. An empty tag
// list means the tags were not fetched; the caller reports that, because
// with no tags at all there is nothing to compare against.
func tagProblems(version string, tags []versionTag, release bool) []string {
	var problems []string
	seen := make(map[string]string)
	var newest [3]int
	newestName := ""
	var current *versionTag
	for i := range tags {
		tg := &tags[i]
		v, ok := parseReleaseVersion(tg.version)
		if !ok {
			problems = append(problems, fmt.Sprintf("tag %s does not carry a X.Y.Z version", tg.name))
			continue
		}
		if prev, dup := seen[tg.version]; dup {
			problems = append(problems, fmt.Sprintf("tags %s and %s both claim %s", prev, tg.name, tg.version))
		}
		seen[tg.version] = tg.name
		if tg.config != "" && tg.config != tg.version {
			problems = append(problems, fmt.Sprintf("tag %s points at a commit whose mygo.json says %s", tg.name, tg.config))
		}
		if newestName == "" || compareReleaseVersion(v, newest) > 0 {
			newest, newestName = v, tg.name
		}
		if tg.version == version {
			current = tg
		}
	}
	if newestName == "" {
		return problems
	}
	cfg, ok := parseReleaseVersion(version)
	if !ok {
		return problems // reported by configProblems
	}
	switch compareReleaseVersion(cfg, newest) {
	case -1:
		problems = append(problems, fmt.Sprintf("mygo.json version %s is older than the newest tag %s", version, newestName))
	case 0:
		if release {
			// A release ships a version nothing has shipped yet.
			problems = append(problems, fmt.Sprintf("mygo.json version %s is already the newest tag %s: bump it for this release", version, newestName))
			break
		}
		// Released, or development after a release: the tag has to be on
		// this history, and its commit has to carry the same version.
		if current == nil {
			problems = append(problems, fmt.Sprintf("no tag for the released version %s", version))
		} else if !current.ancestor {
			problems = append(problems, fmt.Sprintf("tag %s is not on the history of HEAD", current.name))
		}
	}
	return problems
}

func parseReleaseVersion(s string) ([3]int, bool) {
	m := releaseVersionRE.FindStringSubmatch(s)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}, false
		}
		v[i] = n
	}
	return v, true
}

func compareReleaseVersion(a, b [3]int) int {
	for i := range a {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}

func releaseRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "mygo.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("mygo.json not found above %s", dir)
		}
		dir = parent
	}
}

func loadReleaseConfig(t *testing.T, root string) releaseConfig {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "mygo.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c releaseConfig
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("mygo.json: %v", err)
	}
	return c
}

func releaseGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

func readVersionTags(t *testing.T, root string) []versionTag {
	t.Helper()
	var tags []versionTag
	for _, name := range strings.Fields(releaseGit(t, root, "tag", "--list")) {
		version := strings.TrimPrefix(name, "v")
		if _, ok := parseReleaseVersion(version); !ok {
			continue
		}
		tg := versionTag{
			name:    name,
			version: version,
			commit:  releaseGit(t, root, "rev-list", "-n", "1", name),
		}
		// The version that commit itself carried. A tag whose commit says
		// another version is a broken release, whatever mygo.json says now.
		if out, err := exec.Command("git", "-C", root, "show", name+":mygo.json").Output(); err == nil {
			var cfg releaseConfig
			if json.Unmarshal(out, &cfg) == nil {
				tg.config = cfg.Version
			}
		}
		tg.ancestor = gitSucceeds(t, root, "merge-base", "--is-ancestor", tg.commit, "HEAD")
		tags = append(tags, tg)
	}
	return tags
}

// gitSucceeds reports whether git exits 0; exit status 1 is a plain "no",
// anything else is a real failure.
func gitSucceeds(t *testing.T, root string, args ...string) bool {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	err := cmd.Run()
	if err == nil {
		return true
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return false
	}
	t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	return false
}

// releaseCheckContext reports whether this run may publish: mygo refuses
// -upload without the update signing key, so that key marks a release build,
// and the generate directive (mygo runs it through buildCommand) inherits it.
func releaseCheckContext() bool {
	return os.Getenv("MYGO_UPDATER_PRIVATE_KEY") != "" || os.Getenv("RAKKO_RELEASE_CHECK") == "1"
}

func versionProblems(t *testing.T, release bool) []string {
	t.Helper()
	root := releaseRepoRoot(t)
	cfg := loadReleaseConfig(t, root)
	problems := configProblems(cfg)

	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		// A source archive has no tags to compare against; the config
		// checks above still ran.
		t.Log("no .git here: tag checks skipped")
		return problems
	}
	tags := readVersionTags(t, root)
	if len(tags) == 0 {
		problems = append(problems, "no version tags here: run git fetch --tags")
	}
	problems = append(problems, tagProblems(cfg.Version, tags, release)...)
	t.Logf("mygo.json %s, %d version tags, release check %t", cfg.Version, len(tags), release)
	return problems
}

// TestReleaseVersion is the CI check. It accepts both states of a version:
// one just released (equal to the newest tag) and one bumped but not yet
// tagged (newer than the newest tag).
func TestReleaseVersion(t *testing.T) {
	if problems := versionProblems(t, false); len(problems) > 0 {
		t.Fatalf("version configuration is inconsistent:\n - %s", strings.Join(problems, "\n - "))
	}
}

// TestReleaseVersionForRelease runs before an upload and inside
// `mygo build` (the generate directive). A release must ship a version newer
// than every existing tag; a missed bump stops it here. Without a release
// context it skips, so CI and development builds are unaffected.
func TestReleaseVersionForRelease(t *testing.T) {
	if !releaseCheckContext() {
		t.Skip("release check: needs MYGO_UPDATER_PRIVATE_KEY or RAKKO_RELEASE_CHECK=1")
	}
	if problems := versionProblems(t, true); len(problems) > 0 {
		t.Fatalf("this build cannot be released:\n - %s", strings.Join(problems, "\n - "))
	}
}

func TestReleaseVersionRules(t *testing.T) {
	released := versionTag{name: "v1.13.2", version: "1.13.2", commit: "aaa", config: "1.13.2", ancestor: true}
	tests := []struct {
		name    string
		version string
		tags    []versionTag
		release bool
		want    string // a substring of the expected problem; "" means valid
	}{
		{name: "before a release the new tag does not exist yet", version: "1.13.3", tags: []versionTag{released}},
		{name: "on the release commit", version: "1.13.2", tags: []versionTag{released}},
		{name: "development after a release", version: "1.13.2", tags: []versionTag{
			{name: "v1.13.1", version: "1.13.1", config: "1.13.1", ancestor: true},
			released,
		}},
		{name: "version older than the newest tag", version: "1.13.1", tags: []versionTag{released}, want: "older than the newest tag"},
		{name: "tag points at another version", version: "1.13.3", tags: []versionTag{
			{name: "v1.13.2", version: "1.13.2", config: "1.13.1", ancestor: true},
		}, want: "mygo.json says 1.13.1"},
		{name: "released tag is not on this history", version: "1.13.2", tags: []versionTag{
			{name: "v1.13.2", version: "1.13.2", config: "1.13.2", ancestor: false},
		}, want: "not on the history of HEAD"},
		{name: "two tags for one version", version: "1.13.3", tags: []versionTag{
			released,
			{name: "release-1.13.2", version: "1.13.2", config: "1.13.2", ancestor: true},
		}, want: "both claim 1.13.2"},
		{name: "a release must bump the version", version: "1.13.2", tags: []versionTag{released}, release: true, want: "bump it for this release"},
		{name: "a bumped version can be released", version: "1.13.3", tags: []versionTag{released}, release: true},
		{name: "a release older than the newest tag is refused", version: "1.13.1", tags: []versionTag{released}, release: true, want: "older than the newest tag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems := strings.Join(tagProblems(tt.version, tt.tags, tt.release), "\n")
			if tt.want == "" {
				if problems != "" {
					t.Fatalf("expected no problems, got:\n%s", problems)
				}
				return
			}
			if !strings.Contains(problems, tt.want) {
				t.Fatalf("expected a problem containing %q, got:\n%s", tt.want, problems)
			}
		})
	}
}

func goodReleaseConfig() releaseConfig {
	return releaseConfig{
		Name: "Rakko", Slug: "rakko", Identifier: "local.y3.rakko",
		Version: "1.13.2", Main: "./cmd/rakko",
		Updates: &releaseUpdates{GitHub: "henryyu333/rakko", PublicKey: "key"},
	}
}

func TestReleaseConfigRules(t *testing.T) {
	good := goodReleaseConfig()
	if problems := configProblems(good); len(problems) > 0 {
		t.Fatalf("a complete config was rejected: %v", problems)
	}
	for name, mutate := range map[string]func(*releaseConfig){
		"bad version":    func(c *releaseConfig) { c.Version = "1.13" },
		"no updates":     func(c *releaseConfig) { c.Updates = nil },
		"no repository":  func(c *releaseConfig) { c.Updates.GitHub = "" },
		"no signing key": func(c *releaseConfig) { c.Updates.PublicKey = "" },
		"no identity":    func(c *releaseConfig) { c.Identifier = "" },
	} {
		c := goodReleaseConfig()
		mutate(&c)
		if problems := configProblems(c); len(problems) == 0 {
			t.Errorf("%s: expected a problem, got none", name)
		}
	}
}
