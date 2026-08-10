package update

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nccloud/hyperlift-cli/internal/build"
)

// defaultRepo is the GitHub "owner/name" the CLI updates from.
const defaultRepo = "nccloud/hyperlift-cli"

// checksumsFilename is the asset that lists the SHA-256 sum of each file. It
// uses the sha256sum format, one "<hex>  <filename>" per line.
const checksumsFilename = "SHA256SUMS"

// errNoReleases means the repository has no published release yet. GitHub
// answers 404 on the latest-release endpoint.
var errNoReleases = errors.New("no releases published yet")

// release is the part of the GitHub release payload the CLI reads.
type release struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

// releaseAsset is one downloadable file attached to a release.
type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Version returns the release version without a leading "v".
func (r release) Version() string {
	return normalizeVersion(r.TagName)
}

// AssetFor returns the asset for the given GOOS and GOARCH, if there is one. It
// uses the GoReleaser naming style, where the asset name holds the os and arch
// tokens, e.g. hyperlift_darwin_arm64.tar.gz or hyperlift-darwin-arm64. It skips
// the checksum and signature-bundle files.
func (r release) AssetFor(goos, goarch string) (releaseAsset, bool) {
	osToken := strings.ToLower(goos)
	archToken := strings.ToLower(goarch)

	for _, a := range r.Assets {
		name := strings.ToLower(a.Name)
		if name == strings.ToLower(checksumsFilename) {
			continue
		}

		if strings.HasSuffix(name, ".sha256") || strings.HasSuffix(name, ".sig") ||
			strings.HasSuffix(name, ".pem") || strings.HasSuffix(name, ".sigstore.json") {
			continue
		}

		if containsToken(name, osToken) && containsToken(name, archToken) {
			return a, true
		}
	}

	return releaseAsset{}, false
}

// containsToken reports whether name holds tok between two non-alphanumeric
// boundaries. This stops "arm64" from matching inside "arm640".
func containsToken(name, tok string) bool {
	notAlnum := func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
	}

	return slices.Contains(strings.FieldsFunc(name, notAlnum), tok)
}

// releaseSource hides the release backend, so tests can supply a fake.
type releaseSource interface {
	// Latest returns the most recent release, or errNoReleases.
	Latest(ctx context.Context) (release, error)
	// Download reads the bytes of an asset.
	Download(ctx context.Context, a releaseAsset) ([]byte, error)
	// Checksums reads the SHA256SUMS asset of a release and parses it into a
	// map of filename to lowercase hex digest.
	Checksums(ctx context.Context, r release) (map[string]string, error)
}

// githubReleases is the live releaseSource. It calls api.github.com over HTTPS.
type githubReleases struct {
	repo       string
	apiBaseURL string // e.g. https://api.github.com
	httpClient *http.Client
}

// newGitHubReleases builds the production release source for defaultRepo.
func newGitHubReleases() *githubReleases {
	return &githubReleases{
		repo:       defaultRepo,
		apiBaseURL: "https://api.github.com",
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (g *githubReleases) Latest(ctx context.Context) (release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", strings.TrimRight(g.apiBaseURL, "/"), g.repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return release{}, err
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", build.UserAgent())

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return release{}, err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return release{}, errNoReleases
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return release{}, fmt.Errorf("github API %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return release{}, fmt.Errorf("decode release: %w", err)
	}

	if rel.TagName == "" {
		return release{}, errNoReleases
	}

	return rel, nil
}

func (g *githubReleases) Download(ctx context.Context, a releaseAsset) ([]byte, error) {
	return g.fetchBytes(ctx, a.URL)
}

func (g *githubReleases) Checksums(ctx context.Context, r release) (map[string]string, error) {
	var sumsURL string

	for _, a := range r.Assets {
		if strings.EqualFold(a.Name, checksumsFilename) {
			sumsURL = a.URL
			break
		}
	}

	if sumsURL == "" {
		return nil, fmt.Errorf("release %s has no %s asset", r.TagName, checksumsFilename)
	}

	data, err := g.fetchBytes(ctx, sumsURL)
	if err != nil {
		return nil, err
	}

	return parseChecksums(data)
}

func (g *githubReleases) fetchBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", build.UserAgent())

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("download %s: %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}

	return io.ReadAll(resp.Body)
}

// parseChecksums parses sha256sum lines, "<hex>  <name>", into a map of base
// name to lowercase hex digest.
func parseChecksums(data []byte) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		sum := strings.ToLower(fields[0])
		// The rest of the line is the filename. sha256sum marks a binary file
		// with a leading "*".
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		out[name] = sum
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan checksums: %w", err)
	}

	if len(out) == 0 {
		return nil, errors.New("no checksums parsed from SHA256SUMS")
	}

	return out, nil
}
