package update

import (
	"strings"

	"golang.org/x/mod/semver"
)

// normalizeVersion removes a leading "v" and the surrounding whitespace.
func normalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// canonicalV returns v with the leading "v" that golang.org/x/mod/semver needs.
// A release tag carries the "v"; a bare version is also accepted.
func canonicalV(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.HasPrefix(v, "v") {
		return v
	}

	return "v" + v
}

// validVersion reports whether v parses as semver, with an optional leading "v".
// Without this check, a malformed release tag would compare as older than
// everything, and the CLI would report the binary as already up to date.
func validVersion(v string) bool {
	return semver.IsValid(canonicalV(v))
}

// isNewer reports whether latest is strictly newer than current. A current
// version of "dev", or an empty one, is always older than any real release, so a
// local build is always offered an update.
func isNewer(current, latest string) bool {
	latest = canonicalV(latest)
	if latest == "" {
		return false
	}

	current = canonicalV(current)
	if current == "" || current == "vdev" {
		return true
	}

	return semver.Compare(current, latest) < 0
}
