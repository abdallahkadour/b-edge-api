package salonrole

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// allowedSalonOwnerFiles are the only files permitted to reach for a salon's
// owner_id. Everything else must go through Can.
//
// Paths are relative to the repository's internal/ directory.
//
// Exempt a FILE only when the file holds nothing else. artist/repository.go
// was once exempt as a whole for one statement in CreateService; measured on
// 2026-09-26, an inline owner check added anywhere in it passed this test.
// That statement now lives alone in artist/owner_seed.go, and the same probe
// in artist/repository.go fails here, naming the file.
var allowedSalonOwnerFiles = map[string]string{
	"pkg/salonrole/role.go":     "defines Resolve, the one derivation of the role",
	"onboarding/repository.go":  "creates the salon and therefore writes owner_id once",
	"membership/repository.go":  "reads and transfers ownership (Phase 2; may not exist yet)",
	"domain/auth/service.go":    "resolves the role at token issue",
	"domain/auth/repository.go": "loads the owner_id that token issue resolves against",
	"domain/auth/model.go":      "declares the SalonOwnerID field that load scans into",
	"artist/owner_seed.go": "holds ONLY createServiceWithOwnerOfferSQL: CreateService (PP-7) " +
		"identifies the owning artist via salons.owner_id to seed her artist_services row " +
		"in the same statement; a data lookup for that seed, not an authorization decision. " +
		"Its own file so the rest of artist/repository.go stays guarded",
}

// salonOwnerRefs matches a SQL statement or expression that ties the salons
// table to its owner_id column, in either order, within one statement.
//
// Deliberately narrow. internal/media has its own unrelated polymorphic
// owner_id (owner_type/owner_id over media rows) appearing in roughly twenty
// places, and a naive search for "owner_id" would drown this check in false
// positives until someone deleted it.
var salonOwnerRefs = []*regexp.Regexp{
	regexp.MustCompile(`(?s)\bsalons\b[^;]{0,400}?\bowner_id\b`),
	regexp.MustCompile(`(?s)\bowner_id\b[^;]{0,400}?\bsalons\b`),
	regexp.MustCompile(`\bSalonOwnerID\b`),
	regexp.MustCompile(`\bsalons\.owner_id\b`),
}

// TestNoInlineOwnerChecks fails if any file outside the allowlist derives a
// salon role for itself.
//
// This is the enforcement behind the package comment. Without it, "the
// capability matrix lives in one place" is a convention, and this project's
// history is unambiguous about what happens to conventions:
// subscriptionVisibleCond was one rule in three hand-written copies, and
// when it was corrected on 2026-09-20 only one copy was touched. The other
// two were still wrong the following day, leaving a cancelled artist
// reachable through their share link.
//
// Authorisation is a worse thing to get wrong that way than visibility, so
// the rule is a test rather than a comment asking nicely.
func TestNoInlineOwnerChecks(t *testing.T) {
	root := internalDir(t)

	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if _, ok := allowedSalonOwnerFiles[rel]; ok {
			return nil
		}
		if strings.HasSuffix(rel, "_test.go") {
			return nil
		}

		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		body := stripComments(string(src))
		for _, re := range salonOwnerRefs {
			if loc := re.FindStringIndex(body); loc != nil {
				snippet := strings.TrimSpace(collapse(body[loc[0]:loc[1]]))
				if len(snippet) > 120 {
					snippet = snippet[:120] + "..."
				}
				offenders = append(offenders, rel+": "+snippet)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("%d file(s) reach for a salon's owner_id outside "+
			"internal/pkg/salonrole:\n\n  %s\n\n"+
			"Authorisation decisions go through salonrole.Can, and role "+
			"derivation goes through salonrole.Resolve. If one of these is a "+
			"legitimate new home for ownership data - a repository that "+
			"transfers it, say - add it to allowedSalonOwnerFiles with a "+
			"reason. Do not widen the regexes.",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// TestAllowlistHasNoStaleEntries keeps the allowlist honest in the other
// direction. An entry naming a file that no longer exists is permission
// granted to nothing, and it hides the fact that the exception was resolved.
//
// Files marked as future phases are exempt until they land.
func TestAllowlistHasNoStaleEntries(t *testing.T) {
	root := internalDir(t)
	for rel, reason := range allowedSalonOwnerFiles {
		if strings.Contains(reason, "may not exist yet") {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); os.IsNotExist(err) {
			t.Errorf("allowlist names %q, which does not exist - remove the entry "+
				"or mark it as a future phase", rel)
		}
	}
}

func internalDir(t *testing.T) string {
	t.Helper()
	// This test lives at internal/pkg/salonrole, so internal/ is two up.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving internal/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "pkg", "salonrole", "role.go")); err != nil {
		t.Fatalf("expected to find internal/ at %s: %v", root, err)
	}
	return root
}

// stripComments removes // and /* */ comments so that prose explaining the
// rule - including this package's own doc comment, which names salons.owner_id
// repeatedly - is not mistaken for code that applies it.
func stripComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	for i := 0; i < len(src); {
		switch {
		case strings.HasPrefix(src[i:], "//"):
			j := strings.IndexByte(src[i:], '\n')
			if j < 0 {
				return b.String()
			}
			b.WriteByte('\n')
			i += j + 1
		case strings.HasPrefix(src[i:], "/*"):
			j := strings.Index(src[i+2:], "*/")
			if j < 0 {
				return b.String()
			}
			b.WriteByte(' ')
			i += 2 + j + 2
		default:
			b.WriteByte(src[i])
			i++
		}
	}
	return b.String()
}

func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
