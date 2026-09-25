package pricing

// Anti-drift guard: no SQL outside this package may read the price or
// deposit of the services table.
//
// It scans every string literal in internal/ and flags one that names the
// services table (FROM/JOIN services) AND reads price or deposit_amount -
// "final_price" and "effective_price" do not match, because the token must
// not be preceded by a letter or underscore.
//
// Allowlisted by FUNCTION, not file: artist/repository.go holds the owner's
// menu (which must read the salon price) beside code that must not.
//
// Known limitation, stated: a query split across two literals with the table
// in one and the column in the other is not seen. Every current reader keeps
// both in one literal.
//
// PROVEN TO FIRE: see the commit that introduced it.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	reServicesTable = regexp.MustCompile(`(?i)\b(from|join)\s+services\b`)
	reMoneyColumn   = regexp.MustCompile(`(?i)(^|[^a-z_])(price|deposit_amount)\b`)
)

// allowedReaders are functions that legitimately read the SALON's price.
// Entries marked TEMPORARY are removed by the task that migrates them;
// TestAllowlist_EveryEntryStillReadsMoney fails if one is left behind.
var allowedReaders = map[string]string{
	"artist/repository.go:GetServicesBySalon": "the owner's menu screen - it shows and edits the SALON price, which is what it must read",
	"artist/repository.go:GetServiceByID":     "the owner editing one service of the salon menu",
}

func moneyReaders(t *testing.T) map[string]bool {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving internal/: %v", err)
	}
	found := map[string]bool{}
	fset := token.NewFileSet()
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "pkg/pricing/") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil
		}
		for _, decl := range file.Decls {
			// Package-level SQL is scanned too. Several repositories keep
			// column lists as consts (bookingSelectCols, enrichedSelectCols);
			// a guard that only looked inside functions would miss one of
			// those reading services.price outright.
			var name string
			var node ast.Node
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Body == nil {
					continue
				}
				name, node = d.Name.Name, d.Body
			case *ast.GenDecl:
				if len(d.Specs) == 0 {
					continue
				}
				if vs, ok := d.Specs[0].(*ast.ValueSpec); ok && len(vs.Names) > 0 {
					name = vs.Names[0].Name
				} else {
					name = "(decl)"
				}
				node = d
			default:
				continue
			}
			ast.Inspect(node, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if ok && lit.Kind == token.STRING &&
					reServicesTable.MatchString(lit.Value) && reMoneyColumn.MatchString(lit.Value) {
					found[rel+":"+name] = true
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}
	return found
}

func TestNoStrayReadsOfServiceMoney(t *testing.T) {
	var offenders []string
	for where := range moneyReaders(t) {
		if _, ok := allowedReaders[where]; !ok {
			offenders = append(offenders, where)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("these functions read services.price or services.deposit_amount directly:\n\n  %s\n\n"+
			"Use pricing.Price / pricing.Deposit (or pricing.Detail) over a JOIN to "+
			"artist_services. Since migration 052 a price depends on the artist; a "+
			"second calculation is how a customer is shown one price and charged another.",
			strings.Join(offenders, "\n  "))
	}
}

func TestAllowlist_EveryEntryStillReadsMoney(t *testing.T) {
	found := moneyReaders(t)
	for where := range allowedReaders {
		if !found[where] {
			t.Errorf("allowlist entry %q no longer reads service money - delete it", where)
		}
	}
}
