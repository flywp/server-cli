package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SignatureAsset is the signature of the checksum file of a release. The
// maintainer makes it with a key that is kept outside GitHub, after the
// release workflow publishes the release. The agent installs a release by
// itself only when this signature is valid: the checksum file alone proves
// that a download is complete, not who made the release.
const SignatureAsset = ChecksumsAsset + ".sig"

// The lines of a signature file, in this order:
//
//	fly-release-signature-v1
//	key <key id>
//	tag <release tag>
//	signed-at <RFC 3339 time, UTC>
//	sig <base64 ed25519 signature>
//
// The signature covers the first four lines, each with its "\n", followed by
// the exact bytes of checksums.txt. The tag stops a signature from being
// moved to an other release; signed-at is the start of the wait before
// agents install the release.
const signatureHeader = "fly-release-signature-v1"

// maxClockSkew is how far in the future a signature time can be: the clock
// of the maintainer and of the server can differ a little.
const maxClockSkew = 10 * time.Minute

// Signature is a verified signature file.
type Signature struct {
	KeyID    string
	Tag      string
	SignedAt time.Time
}

// KeyID returns the id of a public key: the first 8 bytes of its sha256, in
// hex. A signature names its key with it, so that a new key can replace an
// old one.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// Sign returns the signature file of checksums for the release tag.
func Sign(priv ed25519.PrivateKey, tag string, signedAt time.Time, checksums []byte) []byte {
	pub, _ := priv.Public().(ed25519.PublicKey)
	head := signedHead(KeyID(pub), tag, signedAt.UTC())
	sig := ed25519.Sign(priv, append([]byte(head), checksums...))

	return []byte(head + "sig " + base64.StdEncoding.EncodeToString(sig) + "\n")
}

// Verify checks that sigFile is a valid signature of checksums for the
// release tag, by one of the keys (key id to public key). It refuses a
// signature time more than a few minutes after now.
func Verify(sigFile, checksums []byte, tag string, keys map[string]ed25519.PublicKey, now time.Time) (*Signature, error) {
	lines := strings.Split(string(sigFile), "\n")
	// Five lines, each with its "\n", so the split gives an empty sixth.
	if len(lines) != 6 || lines[5] != "" || lines[0] != signatureHeader {
		return nil, errors.New("the signature file is not in the fly-release-signature-v1 format")
	}

	keyID, ok1 := strings.CutPrefix(lines[1], "key ")
	signedTag, ok2 := strings.CutPrefix(lines[2], "tag ")
	at, ok3 := strings.CutPrefix(lines[3], "signed-at ")
	encoded, ok4 := strings.CutPrefix(lines[4], "sig ")
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return nil, errors.New("the signature file is not in the fly-release-signature-v1 format")
	}

	pub, ok := keys[keyID]
	if !ok {
		return nil, fmt.Errorf("the signature uses the key %q, which this binary does not trust", keyID)
	}
	if signedTag != tag {
		return nil, fmt.Errorf("the signature is for the release %s, not %s", signedTag, tag)
	}
	signedAt, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return nil, fmt.Errorf("the signature time %q is not valid", at)
	}
	sig, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, errors.New("the signature is not a valid ed25519 signature")
	}

	// The head is rebuilt from the lines as they are in the file, so that
	// the check covers exactly the bytes that were signed.
	head := strings.Join(lines[:4], "\n") + "\n"
	if !ed25519.Verify(pub, append([]byte(head), checksums...), sig) {
		return nil, fmt.Errorf("the signature of %s for %s is not correct", ChecksumsAsset, tag)
	}

	// Checked after the signature: only a valid signature makes the time
	// worth an error message.
	if signedAt.After(now.Add(maxClockSkew)) {
		return nil, fmt.Errorf("the signature time %s is in the future", at)
	}

	return &Signature{KeyID: keyID, Tag: signedTag, SignedAt: signedAt}, nil
}

func signedHead(keyID, tag string, signedAt time.Time) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s\nkey %s\ntag %s\nsigned-at %s\n", signatureHeader, keyID, tag, signedAt.Format(time.RFC3339))
	return b.String()
}

// ParseKeys parses a list of public keys: "<key id>:<base64 key>", separated
// with commas. Each id must be the id of its key.
func ParseKeys(list string) (map[string]ed25519.PublicKey, error) {
	keys := make(map[string]ed25519.PublicKey)
	for entry := range strings.SplitSeq(list, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		id, encoded, ok := strings.Cut(entry, ":")
		if !ok {
			return nil, fmt.Errorf("the key %q is not in the <id>:<base64 key> format", entry)
		}
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("the key %s is not a valid ed25519 public key", id)
		}
		pub := ed25519.PublicKey(raw)
		if KeyID(pub) != id {
			return nil, fmt.Errorf("the key id %s does not agree with its key (%s)", id, KeyID(pub))
		}
		keys[id] = pub
	}

	return keys, nil
}

// KeyLine returns the entry of pub for the list that ParseKeys reads.
func KeyLine(pub ed25519.PublicKey) string {
	return KeyID(pub) + ":" + base64.StdEncoding.EncodeToString(pub)
}
