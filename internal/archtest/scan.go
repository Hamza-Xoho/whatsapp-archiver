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
	BannedCalls  map[string]struct{}
	AllowedFiles map[string]struct{}
}
type Violation struct {
	Path   string
	Line   int
	Rule   string
	Detail string
}

func DefaultRules() Rules {
	// Keep this list in sync with .golangci.yml.
	names := []string{"SendMessage", "SendReaction", "SendPresence", "SendChatPresence", "MarkRead", "SetForceActiveDeliveryReceipts", "SendPeerMessage", "UpdateGroupParticipants", "JoinGroupWithLink", "LeaveGroup", "RevokeMessage", "BuildEdit", "CreateGroup", "RejectCall", "Upload", "Logout"}
	banned := make(map[string]struct{}, len(names))
	for _, name := range names {
		banned[name] = struct{}{}
	}
	return Rules{BannedCalls: banned, AllowedFiles: map[string]struct{}{filepath.FromSlash("internal/wa/rerequest.go"): {}}}
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
		if _, allowed := rules.AllowedFiles[rel]; allowed {
			return nil
		}
		ast.Inspect(file, func(node ast.Node) bool {
			sel, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if _, banned := rules.BannedCalls[sel.Sel.Name]; banned {
				out = append(out, violation(set, sel.Sel.Pos(), rel, "banned-call", sel.Sel.Name))
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
