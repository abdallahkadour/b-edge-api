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
// WHAT IT SEES: each string literal, and SQL ASSEMBLED from package string
// constants joined with + or fmt.Sprintf (added 2026-09-26).
// booking/repository.go builds its enriched queries as
// fmt.Sprintf("SELECT %s %s ...", enrichedSelectCols, enrichedFrom); an
// s.price added to the column constant is now named by function. A money
// column counts only when it belongs to services or artist_services -
// unqualified, or qualified by those tables or their aliases - so the
// booking's own b.deposit_amount beside a JOIN on services is not a read.
//
// WHAT IT CANNOT SEE - stated, not hidden: SQL assembled at run time (a
// builder, a joined slice, a parameter), a Sprintf argument or operand that
// is not a package constant (a function-local const included), whole-row
// references (row_to_json(s), SELECT s ... FROM services s, (s).*), `TABLE
// services`, and a comment between JOIN and services. It ignores
// UPDATE/INSERT ... RETURNING and test files. It is a tripwire for the
// common shapes, not a proof; review still has to read the SQL.
//
// PROVEN TO FIRE: the original FROM/JOIN shape in the commit that introduced
// it. The widened shapes (comma join, comma join without a space,
// public.services, "public"."services", \"services\" in an interpreted
// string, SELECT *, s.*, services.*) were each injected into a temporary
// internal/zzprobe/probe.go on 2026-09-26: this version listed all eight by
// function name and failed; the previous version passed with all eight
// present. A split-constant probe in the same file was NOT listed then; that
// limit was closed the same day, and proven on the real code: s.price
// injected into booking's enrichedSelectCols made TestNoStrayReadsOfServiceMoney
// name all five enriched booking functions (restored afterwards).

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

	// artist_services named in a FROM list, with its alias (captured): her
	// raw override beside the salon menu bypasses the resolver as surely as
	// the menu's own price does.
	reArtistServicesAfterFromJoin = regexp.MustCompile(`(?i)\b(?:from|join)\s+(?:only\s+)?` +
		`(?:(?:"public"|public)\s*\.\s*)?(?:"artist_services"|artist_services\b)` +
		`(?:\s+(?:as\s+)?"?([a-z_][a-z0-9_]*)"?)?`)

	// A money column and its qualifier, if any (captured). Not preceded by a
	// letter, digit, underscore, dot or quote, so final_price does not match
	// and ".deposit_amount" cannot match without its qualifier.
	reMoneyToken = regexp.MustCompile(`(?i)(?:^|[^a-z0-9_."])` +
		`(?:"?([a-z_][a-z0-9_]*)"?\s*\.\s*)?"?(?:price|deposit_amount)"?(?:[^a-z0-9_]|$)`)
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
	"offering/repository.go:Upsert": "WRITES artist_services.price/deposit_amount (her override); the " +
		"services join is only the her-current-salon predicate and selects no services column. " +
		"Kept in one literal on purpose - splitting it would only hide it from this guard",
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

	if reSelectStar.MatchString(sql) {
		return true
	}
	// Owner-aware: a money column counts when it is unqualified (ambiguous)
	// or qualified by services, artist_services or one of their aliases -
	// never b.deposit_amount, the deposit stored on a booking.
	owners := map[string]bool{"services": true, "artist_services": true}
	for _, a := range aliases {
		owners[strings.ToLower(a)] = true
	}
	for _, m := range reArtistServicesAfterFromJoin.FindAllStringSubmatch(sql, -1) {
		if m[1] != "" {
			owners[strings.ToLower(m[1])] = true
		}
	}
	for _, m := range reMoneyToken.FindAllStringSubmatch(sql, -1) {
		if m[1] == "" || owners[strings.ToLower(m[1])] {
			return true
		}
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
	return moneyReadersIn(t, root)
}

// moneyReadersIn scans every non-test .go file under root (except
// pkg/pricing) and returns "rel/path.go:Name" for each function or
// package-level declaration whose SQL reads service money. Parameterised on
// root so a test can point it at a throwaway package.
func moneyReadersIn(t *testing.T, root string) map[string]bool {
	t.Helper()
	type parsed struct {
		rel  string
		file *ast.File
	}
	byDir := map[string][]parsed{}
	fset := token.NewFileSet()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
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
		dir := filepath.Dir(path)
		byDir[dir] = append(byDir[dir], parsed{rel, file})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	found := map[string]bool{}
	for _, files := range byDir {
		asts := make([]*ast.File, 0, len(files))
		for _, p := range files {
			asts = append(asts, p.file)
		}
		consts := packageStrings(asts)
		for _, p := range files {
			for _, decl := range p.file.Decls {
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
					switch x := n.(type) {
					case *ast.BasicLit:
						if x.Kind == token.STRING && readsServiceMoney(x.Value) {
							found[p.rel+":"+name] = true
						}
					case *ast.BinaryExpr, *ast.CallExpr:
						// SQL assembled from constants: check the whole query.
						if sql := resolveSQL(x.(ast.Expr), consts); sql != "" && readsServiceMoney(sql) {
							found[p.rel+":"+name] = true
						}
					}
					return true
				})
			}
		}
	}
	return found
}

// packageStrings maps each package-level string constant or variable to its
// text, resolving ones built from other constants with + or fmt.Sprintf.
// Three passes settle declarations that refer to later ones.
func packageStrings(files []*ast.File) map[string]string {
	consts := map[string]string{}
	for pass := 0; pass < 3; pass++ {
		for _, f := range files {
			for _, decl := range f.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
					continue
				}
				for _, spec := range gd.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok || len(vs.Names) != len(vs.Values) {
						continue
					}
					for i, v := range vs.Values {
						if text := resolveSQL(v, consts); text != "" {
							consts[vs.Names[i].Name] = text
						}
					}
				}
			}
		}
	}
	return consts
}

// reFormatVerb is one fmt verb, optionally indexed (%[2]s); %% is literal.
var reFormatVerb = regexp.MustCompile(`%(?:\[(\d+)\])?[-+# 0]*\d*(?:\.\d+)?[a-zA-Z%]`)

// resolveSQL returns the text a string expression evaluates to, as far as
// it can be known statically: literals, package constants, + chains and
// fmt.Sprintf. Anything else (a call such as pricing.Price, a parameter)
// contributes nothing, which can only hide a column, never invent one.
func resolveSQL(e ast.Expr, consts map[string]string) string {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			if u, err := strconv.Unquote(x.Value); err == nil {
				return u
			}
		}
	case *ast.Ident:
		return consts[x.Name]
	case *ast.ParenExpr:
		return resolveSQL(x.X, consts)
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			return resolveSQL(x.X, consts) + resolveSQL(x.Y, consts)
		}
	case *ast.CallExpr:
		sel, ok := x.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Sprintf" || len(x.Args) == 0 {
			return ""
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "fmt" {
			return ""
		}
		args := make([]string, 0, len(x.Args)-1)
		for _, a := range x.Args[1:] {
			args = append(args, resolveSQL(a, consts))
		}
		next := 0
		return reFormatVerb.ReplaceAllStringFunc(resolveSQL(x.Args[0], consts), func(v string) string {
			if v == "%%" {
				return "%"
			}
			i := next
			if m := reFormatVerb.FindStringSubmatch(v); m[1] != "" {
				n, _ := strconv.Atoi(m[1])
				i = n - 1
			}
			next = i + 1
			if i >= 0 && i < len(args) {
				return args[i]
			}
			return ""
		})
	}
	return ""
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

// ── Owner-aware money columns ────────────────────────────────────────────
//
// A money column counts only when it belongs to services or artist_services:
// unqualified, or qualified by one of those tables or their aliases. That is
// what lets the guard check a whole ASSEMBLED query - bookings JOIN services,
// reading the booking's own stored deposit - without a false alarm.

func TestReadsServiceMoney_AnotherTablesQualifiedMoney_NotFlagged(t *testing.T) {
	// b.deposit_amount is the deposit STORED on the booking, not the menu's.
	if readsServiceMoney(`SELECT b.deposit_amount, s.name FROM bookings b JOIN services s ON s.id = b.service_id`) {
		t.Fatal("a money column qualified by another table's alias is not a read of services money")
	}
}

func TestReadsServiceMoney_ArtistServicesAliasMoney_Flagged(t *testing.T) {
	// Her raw override read beside the salon menu bypasses the resolver too.
	if !readsServiceMoney(`SELECT os.price FROM services s JOIN artist_services os ON os.service_id = s.id`) {
		t.Fatal("an artist_services money column beside services must be flagged")
	}
}

func TestReadsServiceMoney_UnqualifiedMoney_Flagged(t *testing.T) {
	// Unqualified is ambiguous, so it counts.
	if !readsServiceMoney(`SELECT name, price FROM services WHERE id = $1`) {
		t.Fatal("an unqualified money column in a services query must be flagged")
	}
}

// ── SQL split across constants ──────────────────────────────────────────
//
// booking/repository.go builds its enriched queries as
// fmt.Sprintf("SELECT %s %s ...", enrichedSelectCols, enrichedFrom): the
// columns in one constant, the JOIN on services in another. Checked one
// literal at a time, adding s.price to the column list would never be seen.
// The guard now resolves string constants joined with + or fmt.Sprintf and
// checks the ASSEMBLED query.

// fakePackage writes one Go file into a throwaway root for moneyReadersIn.
func fakePackage(t *testing.T, src string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "fake")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "repo.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

const splitConsts = "package fake\n\nimport \"fmt\"\n\n" +
	"const cols = `b.id, s.price`\n" +
	"const from = ` FROM bookings b JOIN services s ON s.id = b.service_id`\n\n"

func TestGuard_SplitAcrossConstantsViaSprintf_Flagged(t *testing.T) {
	found := moneyReadersIn(t, fakePackage(t, splitConsts+
		"func Get() string { return fmt.Sprintf(\"SELECT %s %s WHERE b.id = $1\", cols, from) }\n"))
	if !found["fake/repo.go:Get"] {
		t.Fatalf("columns and the services JOIN assembled by fmt.Sprintf must be flagged; found %v", found)
	}
	if found["fake/repo.go:cols"] || found["fake/repo.go:from"] {
		t.Fatalf("neither constant reads service money on its own; found %v", found)
	}
}

func TestGuard_SplitAcrossConstantsViaPlus_Flagged(t *testing.T) {
	found := moneyReadersIn(t, fakePackage(t, splitConsts+
		"var _ = fmt.Sprint\n\nfunc Get() string { return \"SELECT \" + cols + from }\n"))
	if !found["fake/repo.go:Get"] {
		t.Fatalf("columns and the services JOIN assembled with + must be flagged; found %v", found)
	}
}

func TestGuard_SplitBookingShape_NotFlagged(t *testing.T) {
	// The real enriched-booking shape: the booking's OWN stored money beside
	// a JOIN on services for the service's name.
	found := moneyReadersIn(t, fakePackage(t, "package fake\n\nimport \"fmt\"\n\n"+
		"const cols = `b.id, b.final_price, b.deposit_amount, s.name AS service_name`\n"+
		"const from = ` FROM bookings b JOIN services s ON s.id = b.service_id`\n\n"+
		"func Get() string { return fmt.Sprintf(\"SELECT %s %s\", cols, from) }\n"))
	if len(found) != 0 {
		t.Fatalf("a booking's own stored money beside a services join is not a read of service money; found %v", found)
	}
}
