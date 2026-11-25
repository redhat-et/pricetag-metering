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
	"account.html":   "9ec0c16a17aded36ec548073595fee3fc623d0e1e477b766898546cd24cb884d",
	"admin.html":     "3b9004cab7d8f096efcf63c8b243f39dba21fa86c83054f8c6049058051e8d6d",
	"dashboard.html": "cb84b28c486c654020d4b30e72412c4d397e0a1188f0e3d06b954ab70c036bc5",
	"manager.html":   "e61803baa41434fb090bd7122e4278ff8d2994be744f364a833dacc7ea5dfff5",
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
