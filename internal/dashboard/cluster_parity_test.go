package dashboard

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

// clusterGoldenHashes pins each unified template's cluster-mode render to the
// exact bytes origin/main served for that page. The PR contract is that
// cluster output stays byte-identical to main, so a correctly marked but
// whitespace- or content-drifted cluster block must fail CI instead of
// silently shipping a divergent page. The marker scan in mode_test.go cannot
// catch that class of drift; only a byte compare can.
var clusterGoldenHashes = map[string]string{
	"account.html":   "07d21f9ba230dab650012db6853e9d268e8d76f31af1dcd2eb7abec3eae84374",
	"admin.html":     "6351ac44bc58b99294c454af9d361e1fbeba390d1874f5ff4ab059fb921d7ddf",
	"dashboard.html": "859b19e9b22597adf3d5a8443db5cfe2dfaab6eb1f9118516f6608a86c790c85",
	"manager.html":   "4e50f5ffcbd3135e43d5fdddad56df9a69b6a1cfc850e9adbe00306b719aaf1c",
}

// TestClusterModeBytesMatchMain guards the byte-identical guarantee.
func TestClusterModeBytesMatchMain(t *testing.T) {
	for name, want := range clusterGoldenHashes {
		cluster := readMode(t, name, false)
		got := fmt.Sprintf("%x", sha256.Sum256(cluster))
		if got != want {
			t.Errorf("%s: cluster-mode bytes drifted from origin/main (sha256 %s != golden %s)", name, got, want)
		}
	}
}
