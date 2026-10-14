package archtest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"whatsapp-archiver/internal/archtest"
)

func TestScanFlagsClientImportOutsideWA(t *testing.T) {
	v := scanFixture(t, "bad_import.go.txt")
	if len(v) != 1 || v[0].Rule != "client-import" {
		t.Fatalf("violations = %#v", v)
	}
}

func TestScanFlagsBannedCall(t *testing.T) {
	v := scanFixture(t, "bad_send.go.txt")
	if len(v) != 1 || v[0].Rule != "banned-call" {
		t.Fatalf("violations = %#v", v)
	}
}

func TestScanAllowsRetryReceiptInAllowListedFile(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/wa/rerequest.go", "allowed_retry.go.txt")
	v, err := archtest.Scan(root, archtest.DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 0 {
		t.Fatalf("violations = %#v", v)
	}
}

func TestRepoHasNoViolations(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	v, err := archtest.Scan(root, archtest.DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 0 {
		t.Fatalf("violations = %#v", v)
	}
}

func scanFixture(t *testing.T, name string) []archtest.Violation {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, filepath.Join("internal", "scratch", "fixture.go"), name)
	v, err := archtest.Scan(root, archtest.DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func writeFixture(t *testing.T, root, dest, fixture string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, dest)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// CON-1 prefixes and the allow-list, spelled out independently of scan.go so the
// test fails if the rules drift.
var conPrefixes = []string{"Send", "Set", "Update", "Join", "Leave", "Revoke", "Build", "Mark", "Subscribe", "Create", "Reject", "Upload", "Logout"}

var conAllowed = map[string]bool{
	"SetProxy": true, "SetProxyAddress": true, "SetSOCKSProxy": true, "SetMediaHTTPClient": true,
	"SetWebsocketHTTPClient": true, "SetPreLoginHTTPClient": true, "SetMaxParallelRetryReceiptHandling": true,
}

func TestScanFlagsEveryBannedPrefixMethod(t *testing.T) {
	methods := pinnedClientMethods(t)
	if len(methods) < 50 {
		t.Fatalf("only %d banned-prefix Client methods found; wrong whatsmeow directory?", len(methods))
	}
	var src strings.Builder
	src.WriteString("package wa\n\nimport \"go.mau.fi/whatsmeow\"\n\nfunc probe(c *whatsmeow.Client) {\n")
	for _, name := range methods {
		src.WriteString("\tc." + name + "()\n")
	}
	src.WriteString("}\n")
	root := t.TempDir()
	path := filepath.Join(root, "internal", "wa", "probe.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := archtest.Scan(root, archtest.DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	flagged := map[string]bool{}
	for _, x := range v {
		flagged[x.Detail] = true
	}
	for _, name := range methods {
		if conAllowed[name] && flagged[name] {
			t.Errorf("allow-listed %s was flagged", name)
		}
		if !conAllowed[name] && !flagged[name] {
			t.Errorf("%s is not flagged", name)
		}
	}
}

func TestAllowListedFileStillRejectsSendMessage(t *testing.T) {
	const head = "package wa\n\nimport \"go.mau.fi/whatsmeow\"\n\nfunc f(c *whatsmeow.Client) {\n"
	scan := func(file, call string) []archtest.Violation {
		t.Helper()
		root := t.TempDir()
		path := filepath.Join(root, "internal", "wa", file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(head+"\tc."+call+"()\n}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		v, err := archtest.Scan(root, archtest.DefaultRules())
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if v := scan("rerequest.go", "SendMessage"); len(v) != 1 || v[0].Detail != "SendMessage" {
		t.Errorf("SendMessage in rerequest.go: %#v", v)
	}
	if v := scan("rerequest.go", "SetStatusMessage"); len(v) != 1 {
		t.Errorf("SetStatusMessage in rerequest.go: %#v", v)
	}
	if v := scan("rerequest.go", "SendMediaRetryReceipt"); len(v) != 0 {
		t.Errorf("SendMediaRetryReceipt in rerequest.go: %#v", v)
	}
	if v := scan("other.go", "SendMediaRetryReceipt"); len(v) != 1 {
		t.Errorf("SendMediaRetryReceipt in another wa file: %#v", v)
	}
}

func TestRetryReceiptThroughInterfaceOutsideRerequestIsFlagged(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/wa/other.go", "allowed_retry.go.txt")
	v, err := archtest.Scan(root, archtest.DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's interface declares the method (interface-method) and calls it (banned-call).
	rules := map[string]bool{}
	for _, x := range v {
		if x.Detail != "SendMediaRetryReceipt" {
			t.Fatalf("violations = %#v", v)
		}
		rules[x.Rule] = true
	}
	if len(v) != 2 || !rules["banned-call"] || !rules["interface-method"] {
		t.Fatalf("violations = %#v", v)
	}
}

// pinnedClientMethods lists every exported (*Client) method of the pinned
// whatsmeow root package that has a CON-1 banned prefix.
func pinnedClientMethods(t *testing.T) []string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("go not on PATH (run through make): %v", err)
	}
	out, err := exec.Command(goBin, "list", "-m", "-f", "{{.Dir}}", "go.mau.fi/whatsmeow").Output()
	if err != nil {
		t.Fatalf("locating whatsmeow module: %v", err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		t.Fatal("whatsmeow module directory is empty")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	set := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(set, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		if f.Name.Name == "whatsmeow" {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		t.Fatalf("package whatsmeow not found in %s", dir)
	}
	seen := map[string]bool{}
	for _, f := range files {
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 || !fn.Name.IsExported() {
				continue
			}
			star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			if id, ok := star.X.(*ast.Ident); !ok || id.Name != "Client" {
				continue
			}
			for _, p := range conPrefixes {
				if strings.HasPrefix(fn.Name.Name, p) {
					seen[fn.Name.Name] = true
				}
			}
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func TestWAWrapperAndEscapeBypassesAreFlagged(t *testing.T) {
	const wm = "package wa\n\nimport \"go.mau.fi/whatsmeow\"\n\n"
	cases := []struct {
		name, file, src, rule, detail string
	}{
		{"interface wrapper", "wrap.go", "package wa\n\ntype presence interface{ SubscribePresence() error }\n\nfunc use(p presence) { _ = p.SubscribePresence() }\n", "", "SubscribePresence"},
		{"interface declaration only", "decl.go", "package wa\n\ntype namer interface{ SetGroupName() error }\n", "interface-method", "SetGroupName"},
		{"generic constraint", "gen.go", "package wa\n\nfunc call[T interface{ SetGroupName() error }](t T) error { return t.SetGroupName() }\n", "", "SetGroupName"},
		{"embedded client", "emb.go", wm + "type Raw struct{ *whatsmeow.Client }\n", "client-escape", "Client"},
		{"embedded client value", "emb2.go", wm + "type Raw struct{ whatsmeow.Client }\n", "client-escape", "Client"},
		{"type alias", "alias.go", wm + "type raw = whatsmeow.Client\n", "client-escape", "Client"},
		{"exported func result", "fn.go", wm + "type c struct{ client *whatsmeow.Client }\n\nfunc (x *c) Raw() *whatsmeow.Client { return x.client }\n", "client-escape", "Client"},
		{"exported func result in tuple", "fn2.go", wm + "func Raw() (int, *whatsmeow.Client) { return 0, nil }\n", "client-escape", "Client"},
		{"exported field", "field.go", wm + "type S struct{ C *whatsmeow.Client }\n", "client-escape", "Client"},
		{"exported var", "var.go", wm + "var C *whatsmeow.Client\n", "client-escape", "Client"},
		{"exported var of slice", "var2.go", wm + "var Cs []*whatsmeow.Client\n", "client-escape", "Client"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "internal", "wa", c.file)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(c.src), 0o600); err != nil {
				t.Fatal(err)
			}
			v, err := archtest.Scan(root, archtest.DefaultRules())
			if err != nil {
				t.Fatal(err)
			}
			if len(v) == 0 {
				t.Fatal("not flagged")
			}
			for _, x := range v {
				if x.Detail == c.detail && (c.rule == "" || x.Rule == c.rule) {
					return
				}
			}
			t.Fatalf("no %s/%s violation in %#v", c.rule, c.detail, v)
		})
	}
}

func TestWAUnexportedClientFieldAndAllowedInterfaceMethodsPass(t *testing.T) {
	const src = "package wa\n\nimport \"go.mau.fi/whatsmeow\"\n\ntype c struct{ client *whatsmeow.Client }\n\nfunc (x *c) get() *whatsmeow.Client { return x.client }\n\ntype proxy interface{ SetProxyAddress(string) error }\n"
	root := t.TempDir()
	path := filepath.Join(root, "internal", "wa", "ok.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := archtest.Scan(root, archtest.DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 0 {
		t.Fatalf("violations = %#v", v)
	}
}
