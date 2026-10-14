package archtest_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"whatsapp-archiver/internal/archtest"
)

func TestScanFlagsClientImportOutsideWA(t *testing.T) {
	v := scanFixture(t, "bad_import.go.txt")
	if len(v) != 1 || v[0].Rule != "client-import" { t.Fatalf("violations = %#v", v) }
}

func TestScanFlagsBannedCall(t *testing.T) {
	v := scanFixture(t, "bad_send.go.txt")
	if len(v) != 1 || v[0].Rule != "banned-call" { t.Fatalf("violations = %#v", v) }
}

func TestScanAllowsRetryReceiptInAllowListedFile(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/wa/rerequest.go", "allowed_retry.go.txt")
	v, err := archtest.Scan(root, archtest.DefaultRules())
	if err != nil { t.Fatal(err) }
	if len(v) != 0 { t.Fatalf("violations = %#v", v) }
}

func TestRepoHasNoViolations(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	v, err := archtest.Scan(root, archtest.DefaultRules())
	if err != nil { t.Fatal(err) }
	if len(v) != 0 { t.Fatalf("violations = %#v", v) }
}

func scanFixture(t *testing.T, name string) []archtest.Violation {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, filepath.Join("internal", "scratch", "fixture.go"), name)
	v, err := archtest.Scan(root, archtest.DefaultRules())
	if err != nil { t.Fatal(err) }
	return v
}

func writeFixture(t *testing.T, root, dest, fixture string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil { t.Fatal(err) }
	path := filepath.Join(root, dest)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { t.Fatal(err) }
	if err := os.WriteFile(path, data, 0o600); err != nil { t.Fatal(err) }
}
