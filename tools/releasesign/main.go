// Command releasesign makes the release signing key and signs the checksum
// file of a release. The agent installs a release by itself only when the
// signature is valid. It is not part of the fly binary.
//
//	go run ./tools/releasesign keygen -out <private key file> [-comment "server-cli release key for flywp"]
//	go run ./tools/releasesign sign -key <private key file or -> -tag v0.2.1 checksums.txt > checksums.txt.sig
//	go run ./tools/releasesign verify -tag v0.2.1 checksums.txt checksums.txt.sig
//
// Keep the private key outside GitHub, for example in a password manager.
// Never commit it, and never put it in a GitHub secret.
package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flywp/server-cli/internal/release"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "releasesign:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: releasesign keygen|sign|verify [flags]")
	}

	switch args[0] {
	case "keygen":
		return keygen(args[1:], stdout)
	case "sign":
		return sign(args[1:], stdin, stdout)
	case "verify":
		return verify(args[1:], stdout)
	default:
		return fmt.Errorf("unknown command %q: use keygen, sign or verify", args[0])
	}
}

// keygen writes a new private key to a file that must not exist, and prints
// the public key line for internal/release/keys.go. It never prints the
// private key. The comment names the key: it goes in the key file as a PEM
// header, and next to the public key line.
func keygen(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	out := fs.String("out", "", "the file for the private key (it must not exist)")
	comment := fs.String("comment", "", "a name for the key, for example \"server-cli release key for flywp\"")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("keygen needs -out <private key file>")
	}
	path, err := expandHome(*out)
	if err != nil {
		return err
	}
	if strings.ContainsAny(*comment, "\r\n") {
		return errors.New("the comment must be one line")
	}

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	if *comment != "" {
		block.Headers = map[string]string{"Comment": *comment}
	}
	if err := pem.Encode(f, block); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	msg := fmt.Sprintf("Private key: %s (keep it outside GitHub, with a backup)\n", path)
	if *comment != "" {
		msg += fmt.Sprintf("Comment: %s\n", *comment)
	}
	msg += fmt.Sprintf("Public key line for internal/release/keys.go:\n%s\n", release.KeyLine(pub))
	_, err = io.WriteString(stdout, msg)
	return err
}

// sign prints the signature file of a checksum file.
func sign(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	keyPath := fs.String("key", "", "the private key file, or - to read it from stdin")
	tag := fs.String("tag", "", "the release tag, for example v0.2.1")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyPath == "" || *tag == "" || fs.NArg() != 1 {
		return errors.New("usage: releasesign sign -key <file or -> -tag <tag> checksums.txt")
	}

	priv, err := readKey(*keyPath, stdin)
	if err != nil {
		return err
	}
	checksums, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}

	_, err = stdout.Write(release.Sign(priv, *tag, time.Now().UTC().Truncate(time.Second), checksums))
	return err
}

// verify checks a signature file with the keys that this build trusts: the
// keys of the agents that are built from the same commit.
func verify(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	tag := fs.String("tag", "", "the release tag, for example v0.2.1")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tag == "" || fs.NArg() != 2 {
		return errors.New("usage: releasesign verify -tag <tag> checksums.txt checksums.txt.sig")
	}

	keys, err := release.TrustedKeys()
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return errors.New("this build trusts no key: add the public key line to internal/release/keys.go")
	}
	checksums, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	sigFile, err := os.ReadFile(fs.Arg(1))
	if err != nil {
		return err
	}

	sig, err := release.Verify(sigFile, checksums, *tag, keys, time.Now())
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(stdout, "The signature of %s is valid (key %s, signed at %s).\n", *tag, sig.KeyID, sig.SignedAt.Format(time.RFC3339))
	return err
}

// expandHome replaces a leading "~/" with the home directory. A shell does
// not do this in "make release-key KEY=~/key": the "~" is not at the start of
// a word.
func expandHome(path string) (string, error) {
	rest, ok := strings.CutPrefix(path, "~/")
	if !ok {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, rest), nil
}

func readKey(path string, stdin io.Reader) (ed25519.PrivateKey, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(io.LimitReader(stdin, 1<<16))
	} else if path, err = expandHome(path); err == nil {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("the key is not a PEM PRIVATE KEY")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("reading the key: %w", err)
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("the key is not an ed25519 key")
	}

	return priv, nil
}
