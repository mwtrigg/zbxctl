package sshsig

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

// otherKeyLine is a valid ssh-ed25519 public key that did not sign the fixtures.
func otherKeyLine(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var blob bytes.Buffer
	putString(&blob, []byte("ssh-ed25519"))
	putString(&blob, pub)
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob.Bytes()) + " someone else"
}

// testdata/ holds a signature made by `ssh-keygen -Y sign -n zbxctl-release` with a
// throwaway key whose private half was deleted; the public half is test_key.pub.
func fixtures(t *testing.T) (pub string, msg, sig []byte) {
	t.Helper()
	p, err := os.ReadFile("testdata/test_key.pub")
	if err != nil {
		t.Fatal(err)
	}
	msg, _ = os.ReadFile("testdata/checksums.txt")
	sig, _ = os.ReadFile("testdata/checksums.txt.sig")
	return strings.TrimSpace(string(p)), msg, sig
}

func TestVerifiesARealSSHKeygenSignature(t *testing.T) {
	pub, msg, sig := fixtures(t)
	if err := Verify(sig, msg, "zbxctl-release", []string{pub}); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsEveryWayASignatureCanBeWrong(t *testing.T) {
	pub, msg, sig := fixtures(t)
	other := otherKeyLine(t)

	cases := map[string]func() error{
		"changed message": func() error {
			return Verify(sig, append(append([]byte{}, msg...), 'x'), "zbxctl-release", []string{pub})
		},
		"wrong namespace": func() error { return Verify(sig, msg, "git", []string{pub}) },
		"untrusted key":   func() error { return Verify(sig, msg, "zbxctl-release", []string{other}) },
		"no trusted keys": func() error { return Verify(sig, msg, "zbxctl-release", nil) },
		"garbage":         func() error { return Verify([]byte("hello"), msg, "zbxctl-release", []string{pub}) },
		"empty":           func() error { return Verify(nil, msg, "zbxctl-release", []string{pub}) },
		"truncated": func() error {
			s := strings.TrimSpace(string(sig))
			return Verify([]byte(s[:len(s)/2]+"\n-----END SSH SIGNATURE-----"), msg, "zbxctl-release", []string{pub})
		},
		"bad public key line": func() error { return Verify(sig, msg, "zbxctl-release", []string{"ssh-rsa AAAA nope"}) },
	}
	for name, run := range cases {
		if err := run(); err == nil {
			t.Errorf("%s: must fail", name)
		}
	}
	if k := otherKeyLine(t); k == pub {
		t.Fatal("the other key must differ")
	}
	// flip a byte inside the signature body
	b := []byte(sig)
	i := strings.Index(string(b), "\n") + 40
	b[i] ^= 0x01
	if err := Verify(b, msg, "zbxctl-release", []string{pub}); err == nil {
		t.Error("a corrupted signature must fail")
	}
}

func TestParsePublicKey(t *testing.T) {
	pub, _, _ := fixtures(t)
	if k, err := ParsePublicKey(pub); err != nil || len(k) != 32 {
		t.Fatalf("%v %v", k, err)
	}
	for _, bad := range []string{"", "ssh-ed25519", "ssh-ed25519 !!!!", "ssh-rsa AAAAB3NzaC1yc2E"} {
		if _, err := ParsePublicKey(bad); err == nil {
			t.Errorf("%q must fail", bad)
		}
	}
}

func TestSignEd25519RoundTripsAndMatchesTheRealThing(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	msg := []byte("some checksums")
	sig := SignEd25519(priv, msg, "zbxctl-release")
	line := PublicKeyLine(pub, "test")
	if err := Verify(sig, msg, "zbxctl-release", []string{line}); err != nil {
		t.Fatal(err)
	}
	if err := Verify(sig, []byte("changed"), "zbxctl-release", []string{line}); err == nil {
		t.Fatal("a changed message must fail")
	}
	// the armor layout matches what ssh-keygen wrote for the fixture
	_, _, real := fixtures(t)
	if !strings.HasPrefix(string(sig), "-----BEGIN SSH SIGNATURE-----\n") || !strings.HasSuffix(string(sig), "-----END SSH SIGNATURE-----\n") ||
		!strings.HasPrefix(string(real), "-----BEGIN SSH SIGNATURE-----\n") {
		t.Fatal("armor")
	}
}
