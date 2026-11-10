// Package sshsig verifies SSH signatures (the format `ssh-keygen -Y sign` writes,
// described in OpenSSH's PROTOCOL.sshsig) made with ed25519 keys, using only the
// standard library. zbxctl uses it to check that a release's checksums.txt was
// signed by the project's release key before trusting the checksums in it.
package sshsig

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

const (
	armorBegin = "-----BEGIN SSH SIGNATURE-----"
	armorEnd   = "-----END SSH SIGNATURE-----"
	magic      = "SSHSIG"
)

// wire is a reader for SSH wire-format data (uint32 length-prefixed strings).
type wire struct{ b []byte }

func (w *wire) bytes(n int) ([]byte, error) {
	if n < 0 || len(w.b) < n {
		return nil, errors.New("truncated signature")
	}
	out := w.b[:n]
	w.b = w.b[n:]
	return out, nil
}

func (w *wire) uint32() (uint32, error) {
	b, err := w.bytes(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}

func (w *wire) str() ([]byte, error) {
	n, err := w.uint32()
	if err != nil {
		return nil, err
	}
	if n > 1<<20 {
		return nil, errors.New("implausible field length in signature")
	}
	return w.bytes(int(n))
}

func putString(buf *bytes.Buffer, s []byte) {
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(s)))
	buf.Write(l[:])
	buf.Write(s)
}

// ParsePublicKey reads an "ssh-ed25519 AAAA... comment" line and returns the
// 32-byte key.
func ParsePublicKey(line string) (ed25519.PublicKey, error) {
	f := strings.Fields(line)
	if len(f) < 2 || f[0] != "ssh-ed25519" {
		return nil, fmt.Errorf("not an ssh-ed25519 public key: %q", line)
	}
	blob, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil {
		return nil, fmt.Errorf("public key is not valid base64: %w", err)
	}
	return keyFromBlob(blob)
}

func keyFromBlob(blob []byte) (ed25519.PublicKey, error) {
	w := &wire{b: blob}
	typ, err := w.str()
	if err != nil || string(typ) != "ssh-ed25519" {
		return nil, errors.New("signature key is not ssh-ed25519")
	}
	k, err := w.str()
	if err != nil || len(k) != ed25519.PublicKeySize {
		return nil, errors.New("malformed ed25519 public key")
	}
	if len(w.b) != 0 {
		return nil, errors.New("trailing data after the public key")
	}
	return ed25519.PublicKey(k), nil
}

// Verify checks that armored is a valid SSH signature of message in the given
// namespace, made by one of the trusted public keys (authorized-keys style
// lines). Anything else (a malformed signature, a different namespace, an
// untrusted key, a changed message) is an error.
func Verify(armored, message []byte, namespace string, trusted []string) error {
	if len(trusted) == 0 {
		return errors.New("no trusted release keys are configured")
	}
	var keys []ed25519.PublicKey
	for _, line := range trusted {
		k, err := ParsePublicKey(line)
		if err != nil {
			return fmt.Errorf("a built-in trusted key is unreadable: %w", err)
		}
		keys = append(keys, k)
	}

	text := strings.TrimSpace(string(armored))
	if !strings.HasPrefix(text, armorBegin) || !strings.HasSuffix(text, armorEnd) {
		return errors.New("not an SSH signature")
	}
	body := strings.TrimSuffix(strings.TrimPrefix(text, armorBegin), armorEnd)
	raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(body), ""))
	if err != nil {
		return fmt.Errorf("signature is not valid base64: %w", err)
	}

	w := &wire{b: raw}
	m, err := w.bytes(len(magic))
	if err != nil || string(m) != magic {
		return errors.New("not an SSH signature")
	}
	if v, err := w.uint32(); err != nil || v != 1 {
		return errors.New("unsupported SSH signature version")
	}
	pubBlob, err := w.str()
	if err != nil {
		return err
	}
	ns, err := w.str()
	if err != nil {
		return err
	}
	if string(ns) != namespace {
		return fmt.Errorf("signature is for namespace %q, not %q", ns, namespace)
	}
	if _, err := w.str(); err != nil { // reserved
		return err
	}
	hashAlg, err := w.str()
	if err != nil {
		return err
	}
	sigBlob, err := w.str()
	if err != nil {
		return err
	}
	if len(w.b) != 0 {
		return errors.New("trailing data after the signature")
	}

	var digest []byte
	switch string(hashAlg) {
	case "sha512":
		d := sha512.Sum512(message)
		digest = d[:]
	case "sha256":
		d := sha256.Sum256(message)
		digest = d[:]
	default:
		return fmt.Errorf("unsupported hash %q in the signature", hashAlg)
	}

	signer, err := keyFromBlob(pubBlob)
	if err != nil {
		return err
	}
	trustedKey := false
	for _, k := range keys {
		if k.Equal(signer) {
			trustedKey = true
			break
		}
	}
	if !trustedKey {
		return errors.New("signed by a key that is not a trusted release key")
	}

	sw := &wire{b: sigBlob}
	sigType, err := sw.str()
	if err != nil || string(sigType) != "ssh-ed25519" {
		return errors.New("signature is not ssh-ed25519")
	}
	sig, err := sw.str()
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("malformed ed25519 signature")
	}

	// The signed data: "SSHSIG" || namespace || reserved || hash algorithm || H(message)
	var signed bytes.Buffer
	signed.WriteString(magic)
	putString(&signed, ns)
	putString(&signed, nil)
	putString(&signed, hashAlg)
	putString(&signed, digest)
	if !ed25519.Verify(signer, signed.Bytes(), sig) {
		return errors.New("the signature does not match the message")
	}
	return nil
}

// SignEd25519 produces an armored SSH signature of message in the given
// namespace, the same format `ssh-keygen -Y sign` writes. Releases are signed
// with ssh-keygen; this exists so tests (and tools) can sign without it.
func SignEd25519(priv ed25519.PrivateKey, message []byte, namespace string) []byte {
	pub := priv.Public().(ed25519.PublicKey)
	var pubBlob bytes.Buffer
	putString(&pubBlob, []byte("ssh-ed25519"))
	putString(&pubBlob, pub)

	digest := sha512.Sum512(message)
	var signed bytes.Buffer
	signed.WriteString(magic)
	putString(&signed, []byte(namespace))
	putString(&signed, nil)
	putString(&signed, []byte("sha512"))
	putString(&signed, digest[:])
	sig := ed25519.Sign(priv, signed.Bytes())

	var sigBlob bytes.Buffer
	putString(&sigBlob, []byte("ssh-ed25519"))
	putString(&sigBlob, sig)

	var raw bytes.Buffer
	raw.WriteString(magic)
	var v [4]byte
	binary.BigEndian.PutUint32(v[:], 1)
	raw.Write(v[:])
	putString(&raw, pubBlob.Bytes())
	putString(&raw, []byte(namespace))
	putString(&raw, nil)
	putString(&raw, []byte("sha512"))
	putString(&raw, sigBlob.Bytes())

	enc := base64.StdEncoding.EncodeToString(raw.Bytes())
	var out strings.Builder
	out.WriteString(armorBegin + "\n")
	for len(enc) > 70 {
		out.WriteString(enc[:70] + "\n")
		enc = enc[70:]
	}
	out.WriteString(enc + "\n" + armorEnd + "\n")
	return []byte(out.String())
}

// PublicKeyLine renders an ed25519 public key as an authorized_keys style line.
func PublicKeyLine(pub ed25519.PublicKey, comment string) string {
	var blob bytes.Buffer
	putString(&blob, []byte("ssh-ed25519"))
	putString(&blob, pub)
	return strings.TrimSpace("ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob.Bytes()) + " " + comment)
}
