package release

import "crypto/ed25519"

// trustedKeys are the public keys of the release signatures, in the format of
// ParseKeys. The private keys are kept outside GitHub: see "make release-key"
// and "make sign-release". Two keys can be listed while one replaces the other.
//
// Give each key a comment that names it (the COMMENT of make release-key).
//
// It is a string, not a map, so that a test build can set it with
// -ldflags "-X github.com/flywp/server-cli/internal/release.trustedKeys=...".
var trustedKeys = ""

// TrustedKeys returns the public keys that the agent accepts for a release
// signature. An empty map means that no release can install by itself.
func TrustedKeys() (map[string]ed25519.PublicKey, error) {
	return ParseKeys(trustedKeys)
}
