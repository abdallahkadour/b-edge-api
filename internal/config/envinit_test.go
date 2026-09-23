package config_test

// Anti-drift guard: no package-level variable may read os.Getenv.
//
// WHY THIS EXISTS
//
// cmd/main.go calls godotenv.Load() inside main(). Go initialises every
// package-level variable BEFORE main() runs. So a variable of the form
//
//	var x = os.Getenv("SOMETHING")
//
// evaluates against an environment that does not yet contain anything from
// .env. It reads empty, silently, and takes whatever fallback the author
// wrote - forever, no matter what the config says.
//
// Four of these existed on 2026-09-23. Three were URL bases, so every
// customer-facing link in a notification would have pointed at localhost in
// any deployment configured through a .env file. The fourth was
// REQUIRE_VERIFIED_PHONE_FOR_INVITE, a security gate that COULD NOT BE
// SWITCHED ON: setting it to true changed nothing, measured by turning it on
// and watching an unverified artist be invited anyway.
//
// A gate that silently cannot be enabled is worse than one that is off,
// because the configuration claims otherwise.
//
// The fix in every case is to read at CALL time. This test stops the pattern
// coming back.
//
// PROVEN TO FIRE: reintroducing
// `var probe = os.Getenv("X")` anywhere under internal/ fails this test
// naming the file and line.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoPackageLevelEnvReads(t *testing.T) {
	root := repoRootFrom(t, "..", "..")

	var offenders []string
	fset := token.NewFileSet()

	err := filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil // a file excluded by build tags is not this test's business
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, val := range vs.Values {
					if containsGetenv(val) {
						rel, _ := filepath.Rel(root, path)
						offenders = append(offenders,
							rel+":"+itoa(fset.Position(val.Pos()).Line)+" — var "+vs.Names[0].Name)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf(
			"these package-level variables read the environment at init time, "+
				"BEFORE godotenv.Load() runs in main():\n\n  %s\n\n"+
				"They will read empty and silently take their fallback, whatever "+
				"the configuration says. Read the variable at CALL time instead - "+
				"make it a function.",
			strings.Join(offenders, "\n  "))
	}
}

// containsGetenv reports whether an expression anywhere calls os.Getenv -
// including inside an immediately-invoked func literal, which is how three of
// the original four were written and would otherwise hide from a shallow check.
func containsGetenv(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if ok && pkg.Name == "os" && (sel.Sel.Name == "Getenv" || sel.Sel.Name == "LookupEnv") {
			found = true
			return false
		}
		return true
	})
	return found
}

func repoRootFrom(t *testing.T, parts ...string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(append([]string{wd}, parts...)...)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
