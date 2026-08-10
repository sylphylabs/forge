package polaris

import (
	"os"
	"testing"
)

// integrationEnv names the variable that opts a run in to the tests requiring a
// live Polaris server.
const integrationEnv = "FORGE_POLARIS_INTEGRATION"

// requirePolaris skips the calling test unless Polaris integration was enabled.
//
// These tests drive a real server on the console and discovery ports declared
// in this package. Without one they fail on a dial error, which reports an
// absent dependency as if the code were broken and leaves a permanently red
// suite that stops being read. Skipping states the requirement instead, and
// naming the variable in the skip message tells a reader how to run them.
//
// Start a Polaris server on the addresses this package expects, then:
//
//	FORGE_POLARIS_INTEGRATION=1 go test ./...
func requirePolaris(t *testing.T) {
	t.Helper()
	if os.Getenv(integrationEnv) == "" {
		t.Skipf("set %s=1 to run tests that need a live Polaris server", integrationEnv)
	}
}
