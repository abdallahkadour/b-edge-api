package subscription

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ── The fragment itself ───────────────────────────────────────────────────

func TestVisibleArtistCond_UsesTheGivenAlias(t *testing.T) {
	got := VisibleArtistCond("art")
	if !strings.Contains(got, "sub.artist_id = art.id") {
		t.Fatalf("alias not applied:\n%s", got)
	}
	if strings.Contains(got, "a.id") {
		t.Errorf("a hardcoded alias survived; a wrong alias silently matches "+
			"nothing, which in a visibility filter is an empty Discover page "+
			"with no error:\n%s", got)
	}
}

func TestVisibleArtistCond_InterpolatesTheRealGraceWindow(t *testing.T) {
	got := VisibleArtistCond("a")
	want := "INTERVAL '21 days'"
	if !strings.Contains(got, want) {
		t.Errorf("want %q (GraceDays = %d), got:\n%s", want, GraceDays, got)
	}
}

func TestVisibleArtistCond_ReadsTheCompedCodeFromTheConstant(t *testing.T) {
	if !strings.Contains(VisibleArtistCond("a"), "sub.plan_code = '"+CompedPlanCode+"'") {
		t.Error("the comped plan code is written out separately from CompedPlanCode")
	}
}

// TestVisibleArtistCond_HidesCancelled is the specific regression.
//
// The SQL once listed `cancelled_at IS NOT NULL` as a VISIBLE condition, so
// a cancelled artist stayed on Discover. Two of the three copies were still
// wrong a day after the third was fixed, and FRAUD-10 found a cancelled
// artist's share link still rendering a card.
func TestVisibleArtistCond_HidesCancelled(t *testing.T) {
	got := VisibleArtistCond("a")
	if !strings.Contains(got, "sub.cancelled_at IS NULL") {
		t.Error("a cancelled subscription must make the artist invisible")
	}
	if strings.Contains(got, "cancelled_at IS NOT NULL") {
		t.Error("`cancelled_at IS NOT NULL` is back as a visible condition - " +
			"this is the exact defect FRAUD-10 found")
	}
}

// TestVisibleArtistCond_AgreesWithEnforce pins the SQL form against the Go
// form, state by state.
//
// The rule has two expressions - Derive+Enforce in Go for one artist, this
// fragment in SQL for a result set - and nothing but this test stops them
// drifting. It asserts the shape of the SQL rather than executing it, since
// this package has no database; the live behaviour is covered by
// verify-uc6 and security test FRAUD-10.
func TestVisibleArtistCond_AgreesWithEnforce(t *testing.T) {
	cond := VisibleArtistCond("a")

	// Enforce says these three states are visible, and each one has to have
	// a matching arm in the SQL.
	for _, c := range []struct {
		status Status
		clause string
		why    string
	}{
		{StatusTrialing, "sub.trial_ends_at IS NOT NULL AND NOW() < sub.trial_ends_at",
			"a trialing artist"},
		{StatusActive, "NOW() < sub.current_period_end",
			"an artist inside their paid period"},
		{StatusGrace, "INTERVAL '21 days'",
			"an artist inside the grace window"},
	} {
		if !Enforce(c.status).VisibleInDiscovery {
			t.Fatalf("Enforce(%s).VisibleInDiscovery is false - this test's "+
				"premise has changed and the SQL must change with it", c.status)
		}
		if !strings.Contains(cond, c.clause) {
			t.Errorf("Enforce says %s is visible but the SQL has no arm for it "+
				"(%s): missing %q", c.status, c.why, c.clause)
		}
	}

	// And comped, which short-circuits every date check in Derive.
	if !strings.Contains(cond, "sub.plan_code = '"+CompedPlanCode+"'") {
		t.Error("Derive short-circuits on the comped plan; the SQL must too")
	}
}

// ── The anti-drift guard ──────────────────────────────────────────────────

// visibilitySQL matches anyone writing the subscription-VISIBILITY predicate
// out by hand instead of calling VisibleArtistCond.
//
// Matched on the grace-window arithmetic rather than on the table name. The
// first version of this guard looked for `FROM subscriptions sub` and caught
// internal/middleware/billing.go, which is not a duplicate at all: it loads
// a subscription row to feed DeriveStatus in Go, which is exactly the right
// pattern. Allowlisting that file would have taught the next reader it is an
// exception when it is nothing of the kind. `current_period_end + INTERVAL`
// appears only where the grace window is applied inside SQL, which is only
// ever this rule.
var visibilitySQL = regexp.MustCompile(
	`(?s)current_period_end\s*\+\s*INTERVAL|EXISTS\s*\(\s*SELECT 1 FROM subscriptions sub`)

// allowedVisibilityFiles may contain the raw SQL. Exactly one entry, which
// is the point.
var allowedVisibilityFiles = map[string]string{
	"pkg/subscription/visibility.go": "defines it",
}

// TestNoDuplicateVisibilityRule is what replaces the convention.
//
// Until 2026-09-21 this rule lived in three files, each carrying a comment
// asking whoever changed one to remember the others. Nothing enforced it,
// and it drifted - twice. A comment is a request; this is a build failure.
func TestNoDuplicateVisibilityRule(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving internal/: %v", err)
	}

	var offenders []string
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if _, ok := allowedVisibilityFiles[rel]; ok {
			return nil
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if visibilitySQL.MatchString(string(src)) {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("%d file(s) write the subscription-visibility query by hand:\n\n  %s\n\n"+
			"Call subscription.VisibleArtistCond(alias) instead. This rule "+
			"existed as three copies until 2026-09-21; one was fixed and the "+
			"other two were still wrong the next day, leaving a cancelled "+
			"artist reachable through their share link (FRAUD-10).",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}
