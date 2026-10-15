package media

import (
	"testing"

	"go.uber.org/goleak"
)

// M5: every goroutine a test starts must be gone when the package's tests finish.
func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }
