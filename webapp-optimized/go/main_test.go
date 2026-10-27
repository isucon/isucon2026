package isureal

import (
	"os"
	"testing"
)

// The integration tests edit the database directly and expect reads to see
// the edits at once, so the liveness grace of cached images is disabled.
func TestMain(m *testing.M) {
	imageLivenessTTL = 0
	os.Exit(m.Run())
}
