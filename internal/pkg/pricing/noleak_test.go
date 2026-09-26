package pricing

// Anti-drift guard: no SQL outside this package may read the price or
// deposit of the services table.
//
// It scans every string literal in internal/ (non-test .go files, function
// bodies and package-level consts/vars) and flags one that
//
//   - names the services table as a relation: after FROM or JOIN, or after a
//     comma in a FROM list (a comma join); bare, public.-qualified, or
//     double-quoted, including quotes escaped inside a Go interpreted string;
//   - AND reads price or deposit_amount, or selects every column (SELECT *,
//     services.*, or <alias>.* where the alias is bound to services).
//
// "final_price" and "effective_price" do not match, because the token must
// not be preceded by a letter or underscore; count(*) and b.* do not match.
//
// Allowlisted by FUNCTION, not file: artist/repository.go holds the owner's
// menu (which must read the salon price) beside code that must not.
//
// WHAT IT CANNOT SEE - stated, not hidden. It matches one literal at a time,
// so SQL whose table and column live in DIFFERENT literals is invisible:
// two string constants concatenated at the call site (booking/repository.go
// already builds its enriched queries from enrichedSelectCols + enrichedFrom
// this way - enrichedFrom joins services, enrichedSelectCols reads only b.*
// money today; adding s.price to enrichedSelectCols would NOT be caught), a
// table name spliced in with fmt.Sprintf, or SQL assembled at run time. It
// also ignores UPDATE/INSERT ... RETURNING and test files. It is a tripwire
// for the common shapes, not a proof; review still has to read the SQL.
//
// PROVEN TO FIRE: the original FROM/JOIN shape in the commit that introduced
// it. The widened shapes (comma join, comma join without a space,
// public.services, "public"."services", \"services\" in an interpreted
// string, SELECT *, s.*, services.*) were each injected into a temporary
// internal/zzprobe/probe.go on 2026-09-26: this version listed all eight by
// function name and failed; the previous version passed with all eight
// present. A split-constant probe in the same file was NOT listed, which is
// the limit above, measured. The probe was deleted afterwards.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// servicesRelation is the services table as it can be named in a FROM
// list: bare, schema-qualified (public.services) and/or double-quoted
// ("public"."services"), optionally followed by its alias (captured).
const servicesRelation = `(?:(?:"public"|public)\s*\.\s*)?(?:"services"|services\b)` +
	`(?:\s+(?:as\s+)?"?([a-z_][a-z0-9_]*)"?)?`

var (
	// After FROM or JOIN (FROM ONLY included).
	reServicesAfterFromJoin = regexp.MustCompile(`(?i)\b(?:from|join)\s+(?:only\s+)?` + servicesRelation)
	// After a comma - a comma join (FROM bookings b, services s). Only
	// counted when a FROM precedes it in the same literal, so a column list
	// or prose that happens to say ", services" is not a relation.
	reServicesAfterComma = regexp.MustCompile(`(?i),\s*(?:only\s+)?` + servicesRelation)
	reFromKeyword        = regexp.MustCompile(`(?i)\bfrom\b`)

	reMoneyColumn = regexp.MustCompile(`(?i)(^|[^a-z_])(price|deposit_amount)\b`)
	// SELECT * / SELECT DISTINCT * reads every column of every relation in
	// the FROM list, the price included. count(*) is not matched.
	reSelectStar = regexp.MustCompile(`(?i)\bselect\s+(?:distinct\s+)?\*`)
)

// allowedReaders are functions that legitimately read the SALON's price.
// Entries marked TEMPORARY are removed by the task that migrates them;
// TestAllowlist_EveryEntryStillReadsMoney fails if one is left behind.
var allowedReaders = map[string]string{
	"artist/repository.go:GetServicesBySalon": "the owner's menu screen - it shows and edits the SALON price, which is what it must read",
	"artist/repository.go:GetServiceByID":     "the owner editing one service of the salon menu",
}

// readsServiceMoney reports whether one SQL string literal reads the price
// or deposit of the services table: it names services as a relation (FROM,
// JOIN or a comma join; bare, public.-qualified or quoted) AND either names
// price / deposit_amount or selects every column (SELECT *, services.*, or
// <alias>.* where the alias is bound to services).
//
// lit is the literal as it appears in Go source; it is unquoted first, so
// an identifier quoted inside an interpreted string (\"services\") is seen
// as "services".
func readsServiceMoney(lit string) bool {
	sql := lit
	if u, err := strconv.Unquote(lit); err == nil {
		sql = u
	}

	aliases := []string{"services"}
	named := false
	for _, m := range reServicesAfterFromJoin.FindAllStringSubmatch(sql, -1) {
		named = true
		if m[1] != "" {
			aliases = append(aliases, m[1])
		}
	}
	for _, loc := range reServicesAfterComma.FindAllStringSubmatchIndex(sql, -1) {
		if !reFromKeyword.MatchString(sql[:loc[0]]) {
			continue
		}
		named = true
		if loc[2] >= 0 {
			aliases = append(aliases, sql[loc[2]:loc[3]])
		}
	}
	if !named {
		return false
	}

	if reMoneyColumn.MatchString(sql) || reSelectStar.MatchString(sql) {
		return true
	}
	for _, a := range aliases {
		star := regexp.MustCompile(`(?i)(?:^|[^a-z0-9_"])"?` + regexp.QuoteMeta(a) + `"?\s*\.\s*\*`)
		if star.MatchString(sql) {
			return true
		}
	}
	return false
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
				if ok && lit.Kind == token.STRING && readsServiceMoney(lit.Value) {
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

// ── The matcher, shape by shape ──────────────────────────────────────────
//
// Each evasion below was MEASURED against the first version of this guard
// (final review, 2026-09-26): all of them read services.price and none was
// seen. They are pinned here one test per shape, so relaxing the matcher
// fails the build by name.

func TestReadsServiceMoney_JoinOnServices_Flagged(t *testing.T) {
	if !readsServiceMoney(`SELECT s.price FROM bookings b JOIN services s ON s.id = b.service_id`) {
		t.Fatal("a JOIN on services reading s.price must be flagged")
	}
}

func TestReadsServiceMoney_CommaJoin_Flagged(t *testing.T) {
	if !readsServiceMoney(`SELECT s.price FROM bookings b, services s WHERE s.id = b.service_id`) {
		t.Fatal("a comma join (FROM bookings b, services s) must be flagged")
	}
}

func TestReadsServiceMoney_CommaJoinWithoutSpace_Flagged(t *testing.T) {
	if !readsServiceMoney(`SELECT s.deposit_amount FROM bookings b,services s WHERE s.id = b.service_id`) {
		t.Fatal("a comma join with no space after the comma must be flagged")
	}
}

func TestReadsServiceMoney_SchemaQualified_Flagged(t *testing.T) {
	if !readsServiceMoney(`SELECT price FROM public.services WHERE id = $1`) {
		t.Fatal("FROM public.services must be flagged")
	}
}

func TestReadsServiceMoney_QuotedTable_Flagged(t *testing.T) {
	if !readsServiceMoney(`SELECT s.price FROM bookings b JOIN "public"."services" s ON s.id = b.service_id`) {
		t.Fatal(`JOIN "public"."services" must be flagged`)
	}
}

func TestReadsServiceMoney_QuotedTableInInterpretedString_Flagged(t *testing.T) {
	// The scanner sees Go source: in an interpreted string the quotes around
	// an identifier arrive escaped (\"services\"), so the literal must be
	// unquoted before matching.
	if !readsServiceMoney(`"SELECT price FROM \"services\" WHERE id = $1"`) {
		t.Fatal(`FROM \"services\" inside a Go interpreted string must be flagged`)
	}
}

func TestReadsServiceMoney_SelectStar_Flagged(t *testing.T) {
	if !readsServiceMoney(`SELECT * FROM services WHERE salon_id = $1`) {
		t.Fatal("SELECT * FROM services reads every column, the price included")
	}
}

func TestReadsServiceMoney_AliasStar_Flagged(t *testing.T) {
	if !readsServiceMoney(`SELECT b.id, s.* FROM bookings b JOIN services s ON s.id = b.service_id`) {
		t.Fatal("s.* over services reads the price")
	}
}

func TestReadsServiceMoney_TableStar_Flagged(t *testing.T) {
	if !readsServiceMoney(`SELECT services.* FROM services WHERE id = $1`) {
		t.Fatal("services.* reads the price")
	}
}

func TestReadsServiceMoney_BookingPriceColumns_NotFlagged(t *testing.T) {
	// final_price / original_price / effective_price are other tables' or
	// computed columns; the token must not be preceded by a letter or _.
	if readsServiceMoney(`SELECT b.final_price, b.original_price, s.name FROM bookings b JOIN services s ON s.id = b.service_id`) {
		t.Fatal("booking price columns next to a services join are not a read of services.price")
	}
}

func TestReadsServiceMoney_CountStar_NotFlagged(t *testing.T) {
	if readsServiceMoney(`SELECT count(*) FROM services WHERE salon_id = $1`) {
		t.Fatal("count(*) reads no column")
	}
}

func TestReadsServiceMoney_OtherAliasStar_NotFlagged(t *testing.T) {
	if readsServiceMoney(`SELECT b.* FROM bookings b JOIN services s ON s.id = b.service_id`) {
		t.Fatal("b.* is the bookings row, not the services row")
	}
}

func TestReadsServiceMoney_NoServicesTable_NotFlagged(t *testing.T) {
	if readsServiceMoney(`SELECT price, deposit_amount FROM artist_services WHERE artist_id = $1`) {
		t.Fatal("artist_services is not the services table")
	}
}
