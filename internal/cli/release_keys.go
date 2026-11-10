package cli

// releaseKeys are the public keys whose signatures `zbxctl upgrade` accepts on a
// release's checksums.txt. Each line is an ssh-ed25519 public key. More than
// one key may be listed so a key can be rotated: ship a release that trusts both,
// then sign with the new one.
//
// The matching private key is the ZBXCTL_SIGNING_KEY secret of the release
// workflow; it signs checksums.txt (ssh-keygen -Y sign -n zbxctl-release).
var releaseKeys = []string{}
