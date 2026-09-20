package booking

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The column list and the scan functions are hand-maintained and must agree.
// Nothing enforced that, and the failure is silent in the worst way.
//
// WHAT GOES WRONG WITHOUT THIS
//
// `bookingSelectCols` names the columns; `scanBooking` and `scanBookings`
// name the struct fields they land in, positionally. Add a column to one and
// not the other and pgx does not complain about the mismatch you meant - it
// shifts every subsequent field by one, or errors at runtime on the first
// row, depending on where the new column went.
//
// This has nearly happened twice. `reschedule_count` was added to the table
// and would have read zero forever until all three sites were updated by
// hand; `deposit_payer_phone` needed the same three edits in September, and
// the refund gate would have compared against an always-nil payer if one had
// been missed.
//
// A test that reads its own source is unusual. It is used here because the
// relationship being protected is textual - three lists that must stay in
// step - and there is no type the compiler can check it against. The
// alternative was a comment asking people to remember.

func repositorySource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("repository.go")
	if err != nil {
		t.Fatalf("read repository.go: %v", err)
	}
	return string(b)
}

// between returns the text between the first `start` and the next `end`.
func between(t *testing.T, s, start, end string) string {
	t.Helper()
	i := strings.Index(s, start)
	if i < 0 {
		t.Fatalf("could not find %q in repository.go", start)
	}
	rest := s[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		t.Fatalf("could not find %q after %q", end, start)
	}
	return rest[:j]
}

var (
	// A bare column name in the SELECT list: lowercase, no dot prefix.
	columnRe = regexp.MustCompile(`(?m)^\s*([a-z_]+(?:\s*,\s*[a-z_]+)*)\s*,?\s*$`)
	// A scan target: &b.FieldName
	scanTargetRe = regexp.MustCompile(`&b\.([A-Za-z]+)`)
)

func selectColumns(t *testing.T, src string) []string {
	t.Helper()
	block := between(t, src, "const bookingSelectCols = `", "`")
	var out []string
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		for _, c := range strings.Split(line, ",") {
			if c = strings.TrimSpace(c); c != "" {
				out = append(out, c)
			}
		}
	}
	return out
}

func scanTargets(t *testing.T, src, funcSig string) []string {
	t.Helper()
	body := between(t, src, funcSig, "\n}")
	var out []string
	for _, m := range scanTargetRe.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

// The count is the cheap half of the check and catches the common mistake:
// a column added to the SELECT and not to the scan, or the reverse.
func TestBookingSelectCols_MatchesScanBookingArity(t *testing.T) {
	src := repositorySource(t)
	cols := selectColumns(t, src)
	targets := scanTargets(t, src, "func scanBooking(row pgx.Row, b *Booking) error {")

	if len(cols) != len(targets) {
		t.Fatalf(
			"bookingSelectCols has %d columns but scanBooking scans %d fields.\n"+
				"These are positional: a mismatch shifts every later field into the "+
				"wrong one, or fails at runtime on the first row.\n"+
				"columns: %v\nfields:  %v",
			len(cols), len(targets), cols, targets)
	}
}

// The two scanners read the same column list, so they must scan the same
// fields in the same order. One being updated without the other is the
// same defect wearing a different hat.
func TestScanBookingAndScanBookings_ScanIdenticalFields(t *testing.T) {
	src := repositorySource(t)
	one := scanTargets(t, src, "func scanBooking(row pgx.Row, b *Booking) error {")
	many := scanTargets(t, src, "func scanBookings(rows pgx.Rows) ([]*Booking, error) {")

	if len(one) != len(many) {
		t.Fatalf("scanBooking scans %d fields, scanBookings scans %d", len(one), len(many))
	}
	for i := range one {
		if one[i] != many[i] {
			t.Fatalf("scanners diverge at position %d: scanBooking has %s, scanBookings has %s\n"+
				"They read the same SELECT, so the order must be identical.",
				i, one[i], many[i])
		}
	}
}

// Every column in the list must have a plausible field scanning it. This
// catches a rename on one side - the arity check alone would pass.
func TestBookingSelectCols_EveryColumnHasAField(t *testing.T) {
	src := repositorySource(t)
	cols := selectColumns(t, src)
	targets := scanTargets(t, src, "func scanBooking(row pgx.Row, b *Booking) error {")
	if len(cols) != len(targets) {
		t.Skip("arity already covered by TestBookingSelectCols_MatchesScanBookingArity")
	}

	// snake_case -> CamelCase, the convention every field here follows.
	camel := func(s string) string {
		var b strings.Builder
		for _, part := range strings.Split(s, "_") {
			if part == "" {
				continue
			}
			b.WriteString(strings.ToUpper(part[:1]))
			b.WriteString(part[1:])
		}
		return b.String()
	}

	// Columns whose Go field deliberately differs in spelling.
	exceptions := map[string]string{
		"id": "ID", "salon_id": "SalonID", "store_id": "StoreID",
		"artist_id": "ArtistID", "customer_id": "CustomerID",
		"service_id": "ServiceID", "deposit_payer_phone": "DepositPayerPhone",
	}

	for i, col := range cols {
		want := camel(col)
		if e, ok := exceptions[col]; ok {
			want = e
		}
		got := targets[i]
		if !strings.EqualFold(got, want) {
			t.Errorf("position %d: column %q lands in field %q (expected something like %q).\n"+
				"If this is a deliberate spelling, add it to the exceptions map above.",
				i, col, got, want)
		}
	}
}
