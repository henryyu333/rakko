package main

// rakkoUpdateSource is the mygo.Updater source of Rakko's daily checks: the
// website's update proxy first, so the release feed is not hit on every
// check (the proxy dedups by day and passes the signed manifest through
// unchanged), and GitHub's release asset when the proxy fails or is down.
// mygo.Updater still verifies the signature and SHA-256 of everything the
// source serves; the source only decides where the bytes come from.

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/netproxy"
)

const (
	// updateProxyBase is the website's update endpoint; the target and the
	// running version ride on the URL.
	updateProxyBase = "https://rakko-yu.pages.dev/updates/update-"
	// updateGitHubBase is the GitHub release asset the proxy mirrors.
	updateGitHubBase = "https://github.com/henryyu333/rakko/releases/latest/download/update-"
)

// updateSourcePrimaryTimeout bounds the proxy request, so a slow proxy
// cannot spend the check's whole deadline and leave GitHub untried. A
// variable so tests can shrink it.
var updateSourcePrimaryTimeout = 10 * time.Second

// rakkoUpdateSource serves the manifest and the install files mygo's
// updater asks for. The zero value is not usable; newRakkoUpdateSource
// builds the production one.
type rakkoUpdateSource struct {
	// primary and fallback are manifest URLs without the ?v= tag.
	primary, fallback string
	// client honours the same proxy rules as the rest of the app.
	client *http.Client
	// version fills ?v= and the User-Agent: Rakko/<version>.
	version func() string
}

// newRakkoUpdateSource is the source of a release build: the website proxy
// for this binary's platform, GitHub's latest release as the fallback.
func newRakkoUpdateSource() *rakkoUpdateSource {
	target := runtime.GOOS + "-" + runtime.GOARCH
	return &rakkoUpdateSource{
		primary:  updateProxyBase + target + ".json",
		fallback: updateGitHubBase + target + ".json",
		client:   netproxy.NewClient(0),
		version:  appVersion,
	}
}

// installUpdateSource points mygo's updater at the website proxy. The
// end-to-end update test bakes its own loopback feed into the binary and
// drives it through RAKKO_UPDATE_SELFTEST, so that run keeps mygo's feed.
func installUpdateSource() {
	if os.Getenv("RAKKO_UPDATE_SELFTEST") == "1" {
		return
	}
	mygo.Updater.SetSource(newRakkoUpdateSource())
}

// Manifest reads the signed manifest: the proxy first with a short deadline
// of its own, GitHub when the proxy is slow, answers other than 200, or
// fails outright. prereleases is ignored: Rakko offers stable versions only
// (it never calls SetPrereleases), and the proxy mirrors the latest release.
func (s *rakkoUpdateSource) Manifest(ctx context.Context, prereleases bool) (io.ReadCloser, error) {
	version := s.version()
	primaryURL := withUpdateVersion(s.primary, version)
	primaryCtx, cancel := context.WithTimeout(ctx, updateSourcePrimaryTimeout)
	body, err := s.get(primaryCtx, primaryURL, version)
	if err == nil {
		return &updateSourceBody{ReadCloser: body, cancel: cancel}, nil
	}
	cancel()
	if ctx.Err() != nil {
		return nil, err
	}
	fallback := withUpdateVersion(s.fallback, version)
	body, fallbackErr := s.get(ctx, fallback, version)
	if fallbackErr != nil {
		return nil, fmt.Errorf("%w (fallback: %v)", err, fallbackErr)
	}
	return body, nil
}

// Open returns the archive or delta the manifest names. Only HTTPS, plus
// the loopback the end-to-end test serves from, is fetched: a manifest
// cannot redirect the updater to plain HTTP.
func (s *rakkoUpdateSource) Open(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isUpdateLoopback(u.Hostname())) {
		return nil, fmt.Errorf("refusing to download %s: updates need HTTPS", rawURL)
	}
	return s.get(ctx, rawURL, s.version())
}

// get is one GET with Rakko's own User-Agent: GitHub's asset store answers
// 400 to a non-ASCII one, and the proxy keys its daily salt on it too.
func (s *rakkoUpdateSource) get(ctx context.Context, rawURL, version string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Rakko/"+version)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
	}
	return resp.Body, nil
}

// updateSourceBody cancels the primary request when the body closes, so a
// response that arrived inside the short deadline is still read to its end
// (the deadline would otherwise cut the manifest off mid-stream).
type updateSourceBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *updateSourceBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// withUpdateVersion tags a manifest URL with the running version, the
// proxy's other way of learning it besides the User-Agent.
func withUpdateVersion(rawURL, version string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	q.Set("v", version)
	u.RawQuery = q.Encode()
	return u.String()
}

// isUpdateLoopback reports a loopback host, where plain HTTP is allowed for
// the end-to-end test's server.
func isUpdateLoopback(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
