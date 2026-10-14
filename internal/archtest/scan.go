package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
)

type Rules struct {
	// BannedCalls are banned on any receiver, in any file.
	BannedCalls map[string]struct{}
	// BannedPrefixes ban every method with the prefix when called on a
	// non-package receiver in a file that imports the whatsmeow client.
	BannedPrefixes []string
	// AllowedCalls are harmless prefix matches (proxy and HTTP-client setters).
	AllowedCalls map[string]struct{}
	// FileAllowedCalls exempts one call name in one file; nothing else.
	FileAllowedCalls map[string]map[string]struct{}
}
type Violation struct {
	Path   string
	Line   int
	Rule   string
	Detail string
}

func DefaultRules() Rules {
	// Keep these lists in sync with .golangci.yml.
	names := []string{"SendMessage", "SendReaction", "SendPresence", "SendChatPresence", "MarkRead", "SetForceActiveDeliveryReceipts", "SendPeerMessage", "UpdateGroupParticipants", "JoinGroupWithLink", "LeaveGroup", "RevokeMessage", "BuildEdit", "CreateGroup", "RejectCall", "Upload", "Logout", "SendMediaRetryReceipt"}
	banned := make(map[string]struct{}, len(names))
	for _, name := range names {
		banned[name] = struct{}{}
	}
	allowed := map[string]struct{}{}
	for _, name := range []string{"SetProxy", "SetProxyAddress", "SetSOCKSProxy", "SetMediaHTTPClient", "SetWebsocketHTTPClient", "SetPreLoginHTTPClient", "SetMaxParallelRetryReceiptHandling"} {
		allowed[name] = struct{}{}
	}
	return Rules{
		BannedCalls:      banned,
		BannedPrefixes:   []string{"Send", "Set", "Update", "Join", "Leave", "Revoke", "Build", "Mark", "Subscribe", "Create", "Reject", "Upload", "Logout"},
		AllowedCalls:     allowed,
		FileAllowedCalls: map[string]map[string]struct{}{filepath.FromSlash("internal/wa/rerequest.go"): {"SendMediaRetryReceipt": {}}},
	}
}

func (r Rules) bannedPrefix(name string) bool {
	if _, ok := r.AllowedCalls[name]; ok {
		return false
	}
	for _, p := range r.BannedPrefixes {
		// The prefix must end a word: Sender and Settings are fields, not sends.
		if rest, ok := strings.CutPrefix(name, p); ok && (rest == "" || (rest[0] >= 'A' && rest[0] <= 'Z')) {
			return true
		}
	}
	return false
}

// importNames returns the local package names a file's imports introduce.
func importNames(file *ast.File) map[string]struct{} {
	out := map[string]struct{}{}
	for _, imp := range file.Imports {
		if imp.Name != nil {
			out[imp.Name.Name] = struct{}{}
			continue
		}
		path, _ := strconv.Unquote(imp.Path.Value)
		out[filepath.Base(path)] = struct{}{}
	}
	return out
}

func Scan(root string, rules Rules) ([]Violation, error) {
	var out []Violation
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case "vendor", ".cache", ".tools", "data", "testdata":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, path, nil, 0)
		if err != nil {
			return err
		}
		inWA := strings.HasPrefix(filepath.ToSlash(rel), "internal/wa/")
		for _, imp := range file.Imports {
			name, _ := strconv.Unquote(imp.Path.Value)
			if name == "go.mau.fi/whatsmeow" && !inWA {
				out = append(out, violation(set, imp.Pos(), rel, "client-import", name))
			}
		}
		importsClient := false
		wmName := "whatsmeow"
		for _, imp := range file.Imports {
			if name, _ := strconv.Unquote(imp.Path.Value); name == "go.mau.fi/whatsmeow" {
				importsClient = true
				if imp.Name != nil {
					wmName = imp.Name.Name
				}
			}
		}
		// Every non-test file in internal/wa may hold a *whatsmeow.Client passed in
		// from another file, through an interface or a type parameter, so the prefix
		// rule does not depend on this file importing the client.
		waProd := inWA && !strings.HasSuffix(rel, "_test.go")
		prefixRule := importsClient || waProd
		pkgNames := importNames(file)
		fileAllowed := rel2allowed(rules, rel)
		exempt := func(name string) bool { _, ok := fileAllowed[name]; return ok }
		ast.Inspect(file, func(node ast.Node) bool {
			if waProd {
				out = append(out, escapes(set, rel, node, wmName, rules, exempt)...)
			}
			sel, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			name := sel.Sel.Name
			if exempt(name) {
				return true
			}
			if _, banned := rules.BannedCalls[name]; banned {
				out = append(out, violation(set, sel.Sel.Pos(), rel, "banned-call", name))
				return true
			}
			// A prefix hit on a receiver that is not a whatsmeow client (a struct
			// field, say) is a false positive; it fails safe and is fixed by renaming.
			if prefixRule && rules.bannedPrefix(name) {
				if id, isIdent := sel.X.(*ast.Ident); isIdent {
					if _, isPkg := pkgNames[id.Name]; isPkg {
						return true // package-level function, not a client method
					}
				}
				out = append(out, violation(set, sel.Sel.Pos(), rel, "banned-call", name))
			}
			return true
		})
		return nil
	})
	return out, err
}

func violation(set *token.FileSet, pos token.Pos, path, rule, detail string) Violation {
	return Violation{Path: path, Line: set.Position(pos).Line, Rule: rule, Detail: detail}
}

func rel2allowed(rules Rules, rel string) map[string]struct{} { return rules.FileAllowedCalls[rel] }

// mentionsClient reports whether the type expression is or contains whatsmeow.Client.
func mentionsClient(expr ast.Expr, wm string) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "Client" {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == wm {
				found = true
			}
		}
		return !found
	})
	return found
}

// escapes reports, for internal/wa files, interface or constraint methods with a
// banned name and any way the raw client can leave the package.
func escapes(set *token.FileSet, rel string, node ast.Node, wm string, rules Rules, exempt func(string) bool) []Violation {
	var out []Violation
	add := func(pos token.Pos, rule string) {
		out = append(out, violation(set, pos, rel, rule, "Client"))
	}
	switch n := node.(type) {
	case *ast.InterfaceType:
		for _, m := range n.Methods.List {
			for _, name := range m.Names {
				_, banned := rules.BannedCalls[name.Name]
				if (banned || rules.bannedPrefix(name.Name)) && !exempt(name.Name) {
					out = append(out, violation(set, name.Pos(), rel, "interface-method", name.Name))
				}
			}
		}
	case *ast.TypeSpec:
		if n.Assign.IsValid() && mentionsClient(n.Type, wm) {
			add(n.Pos(), "client-escape")
		}
	case *ast.StructType:
		for _, f := range n.Fields.List {
			if !mentionsClient(f.Type, wm) {
				continue
			}
			exported := false
			for _, name := range f.Names {
				exported = exported || name.IsExported()
			}
			if len(f.Names) == 0 || exported { // embedded, or an exported field
				add(f.Pos(), "client-escape")
			}
		}
	case *ast.FuncDecl:
		if n.Name.IsExported() && n.Type.Results != nil {
			for _, r := range n.Type.Results.List {
				if mentionsClient(r.Type, wm) {
					add(r.Pos(), "client-escape")
				}
			}
		}
	case *ast.ValueSpec:
		if n.Type != nil && mentionsClient(n.Type, wm) {
			for _, name := range n.Names {
				if name.IsExported() {
					add(name.Pos(), "client-escape")
				}
			}
		}
	}
	return out
}
