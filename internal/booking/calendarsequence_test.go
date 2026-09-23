package booking

// Anti-drift guard: every statement that moves a booking's time must bump
// calendar_sequence.
//
// Migration 031's header states the rule, CLAUDE.md repeats it, and
// repository.go's ShiftBookings comment repeats it a third time:
//
//	"ANY code that changes a booking's start_time or end_time MUST also
//	 increment calendar_sequence in the same statement."
//
// It was stated in three places and enforced in one. ShiftBookings had it;
// RescheduleBooking - the function named for the exact case the rule describes
// - did not, from migration 031 until 2026-09-23. A rescheduled appointment
// went out with an unchanged SEQUENCE, so per RFC 5545 a calendar client is
// entitled to ignore the update entirely: the customer's phone keeps showing
// the OLD time and she arrives when the artist is not expecting her.
//
// A prose rule in three files is not enforcement. This is: it parses the
// package's own SQL and fails the build if a new statement moves those columns
// without bumping the sequence. That is the remedy this project has adopted for
// the defect class - a rule written correctly in one place and not consulted in
// another - because the alternative is finding the next instance in production.
//
// PROVEN TO FIRE: deleting the `calendar_sequence = calendar_sequence + 1` line
// from RescheduleBooking makes this test fail with that statement named.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestEveryBookingTimeWriteBumpsCalendarSequence(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}

	var offenders []string

	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}

				sql := lit.Value
				if !writesBookingTime(sql) {
					return true
				}
				if strings.Contains(sql, "calendar_sequence") {
					return true
				}

				offenders = append(offenders,
					fset.Position(lit.Pos()).String()+" — "+firstSQLLine(sql))
				return true
			})
		}
	}

	if len(offenders) > 0 {
		t.Fatalf(
			"these SQL statements move a booking's time without incrementing "+
				"calendar_sequence:\n\n  %s\n\n"+
				"Migration 031: ANY code that changes start_time or end_time MUST "+
				"also increment calendar_sequence IN THE SAME STATEMENT. Without it "+
				"a calendar client may ignore the update, leaving the customer's "+
				"phone showing the old time.",
			strings.Join(offenders, "\n  "))
	}
}

// writesBookingTime reports whether a SQL literal assigns start_time or
// end_time on a bookings UPDATE.
//
// Deliberately narrow. It looks for the column on the LEFT of an assignment,
// so `WHERE start_time > $1` and `ORDER BY start_time` are not matched, and it
// requires the statement to target `bookings` - internal/artist writes
// start_time on artist_schedule_exceptions, which has no calendar_sequence and
// is not an appointment.
func writesBookingTime(sql string) bool {
	lower := strings.ToLower(sql)
	if !strings.Contains(lower, "update bookings") {
		return false
	}
	for _, col := range []string{"start_time", "end_time"} {
		idx := 0
		for {
			i := strings.Index(lower[idx:], col)
			if i < 0 {
				break
			}
			pos := idx + i + len(col)
			// Skip whitespace, then require '=' but not '>=', '<=' or '=='.
			j := pos
			for j < len(lower) && (lower[j] == ' ' || lower[j] == '\t') {
				j++
			}
			if j < len(lower) && lower[j] == '=' {
				prev := strings.TrimRight(lower[idx+i-2:idx+i], " \t")
				if !strings.HasSuffix(prev, ">") && !strings.HasSuffix(prev, "<") {
					return true
				}
			}
			idx = pos
		}
	}
	return false
}

func firstSQLLine(sql string) string {
	for _, line := range strings.Split(sql, "\n") {
		line = strings.TrimSpace(strings.Trim(line, "`\""))
		if line != "" {
			if len(line) > 80 {
				line = line[:80] + "…"
			}
			return line
		}
	}
	return "(empty)"
}
