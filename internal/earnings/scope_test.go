package earnings

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ── The predicates ────────────────────────────────────────────────────────

func TestEarnedCond_ReadsRevenueStatuses(t *testing.T) {
	got := EarnedCond("")
	for _, s := range RevenueStatuses {
		if !strings.Contains(got, "'"+s+"'") {
			t.Errorf("RevenueStatuses contains %q but the predicate does not: %s", s, got)
		}
	}
}

// Adding a status to RevenueStatuses must change every aggregate, which is
// the entire point of the extraction.
func TestEarnedCond_FollowsRevenueStatuses(t *testing.T) {
	original := RevenueStatuses
	t.Cleanup(func() { RevenueStatuses = original })

	RevenueStatuses = []string{"completed", "no_show", "settled"}
	if !strings.Contains(EarnedCond(""), "'settled'") {
		t.Error("a status added to RevenueStatuses did not reach the predicate")
	}
}

func TestEarnedCond_AppliesTheAlias(t *testing.T) {
	if got := EarnedCond("b"); !strings.HasPrefix(got, "b.status IN") {
		t.Errorf("alias not applied: %s", got)
	}
	if got := EarnedCond(""); !strings.HasPrefix(got, "status IN") {
		t.Errorf("empty alias must produce a bare column: %s", got)
	}
}

// Half-open, and this is the whole reason it is a named function. A closed
// upper bound counts a booking starting exactly at midnight into both the
// month that ends and the month that begins, so the year total comes out
// larger than the sum of the months.
func TestPeriodCond_IsHalfOpen(t *testing.T) {
	got := PeriodCond("", 2, 3)
	if !strings.Contains(got, "start_time >= $2") {
		t.Errorf("lower bound must be inclusive: %s", got)
	}
	if !strings.Contains(got, "start_time < $3") {
		t.Errorf("upper bound must be EXCLUSIVE, or periods double-count their "+
			"boundary: %s", got)
	}
	if strings.Contains(got, "<= $3") {
		t.Error("the upper bound became inclusive")
	}
}

func TestPeriodCond_UsesTheGivenPlaceholders(t *testing.T) {
	got := PeriodCond("b", 4, 5)
	if !strings.Contains(got, "b.start_time >= $4") || !strings.Contains(got, "b.start_time < $5") {
		t.Errorf("placeholders or alias wrong: %s", got)
	}
}

func TestArtistScopeCond_UsesTheGivenPlaceholder(t *testing.T) {
	if got := ArtistScopeCond("b", 1); got != "b.artist_id = $1" {
		t.Errorf("got %q", got)
	}
	if got := ArtistScopeCond("", 7); got != "artist_id = $7" {
		t.Errorf("got %q", got)
	}
}

// ── The anti-drift guard ──────────────────────────────────────────────────

// revenueStatusSQL matches the status list written out by hand instead of
// taken from RevenueStatuses.
var revenueStatusSQL = regexp.MustCompile(
	`status\s+IN\s*\(\s*'completed'\s*,\s*'no_show'\s*\)`)

// TestNoHardcodedRevenueStatuses.
//
// RevenueStatuses has declared which bookings count as earned since this
// domain was written, and until 2026-09-21 it was read by nothing: all three
// aggregate queries hard-coded the identical list a few lines below it.
// Nothing had gone wrong, which is the only reason that was a tidy-up
// rather than an incident - the same shape in internal/discovery left a
// cancelled artist bookable through their share link for a day.
func TestNoHardcodedRevenueStatuses(t *testing.T) {
	root, err := filepath.Abs("..")
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
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		// Comments are stripped first. scope.go's own doc comment quotes the
		// literal while explaining why it must not be written by hand, and
		// a guard that flags the file documenting the rule teaches people to
		// delete the guard. internal/pkg/salonrole's nodrift_test.go does the
		// same for the same reason.
		if revenueStatusSQL.MatchString(stripComments(string(src))) {
			rel, _ := filepath.Rel(root, path)
			offenders = append(offenders, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("%d file(s) hard-code the revenue status list:\n\n  %s\n\n"+
			"Use earnings.EarnedCond(alias), which reads RevenueStatuses. "+
			"Otherwise adding a status changes some aggregates and not others, "+
			"and the totals stop agreeing with each other.",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// stripComments removes // and /* */ comments so prose explaining a rule is
// not mistaken for code applying it.
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
