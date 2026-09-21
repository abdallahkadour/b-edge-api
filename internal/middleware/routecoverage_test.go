// routecoverage_test.go asserts that every salon-scoped mutating route
// carries a capability guard.
//
// The capability matrix being correct is worth nothing if a route forgets to
// consult it, and a missing guard is invisible in review: the route works, the
// tests pass, and the hole only shows up when a second artist joins a salon
// and edits the owner's prices. This test reads the route registrations out of
// the source so that adding an unguarded salon write fails the build.
//
// It is source-parsing rather than reflective because Fiber's GetRoutes()
// exposes a handler count, not handler identities - there is no way to ask a
// registered route whether RequireSalonCapability is in its chain. The same
// technique is used by internal/booking/columns_test.go, which parses its own
// source to keep bookingSelectCols and scanBooking in agreement.
package middleware

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// exemptRoutes are salon-scoped mutating routes that deliberately carry no
// capability guard. Every entry needs a reason, and the reason has to be about
// the route rather than about it being inconvenient to guard.
var exemptRoutes = map[string]string{
	// Leaving is the one membership write a member performs on THEMSELVES.
	// Guarding it with MembersWrite would mean only the owner could leave,
	// which is the opposite of the intent. Its own refusals are in the
	// service: an owner must transfer first (BR-2) and the last member
	// cannot empty the salon (BR-3).
	"POST /api/v1/artists/salon/members/leave": "a member acting on their own membership, not the roster",

	"PATCH /api/v1/artists/salon/orders/:id/ship":    "logistics, no money, reversible - a member holding the parcel should mark it shipped",
	"PATCH /api/v1/artists/salon/orders/:id/deliver": "same as ship",

	// Billing is still keyed on subscriptions.artist_id, so these act on the
	// CALLER's own subscription, not the salon's. Guarding them with an
	// owner-only capability today would stop a member paying for themselves.
	// They become salon-grained in Phase 3 (migrations 049-051) and get
	// BillingWrite in the same change - see
	// B-Edge-Multi-Artist-Salon-Plan-v1.md task T3.3.
	"POST /api/v1/billing/invoices/:id/submit": "per-artist subscription until Phase 3 regrains it to the salon",
}

var (
	reGroup  = regexp.MustCompile(`(\w+)\s*:=\s*app\.Group\(\s*"([^"]+)"`)
	reConst  = regexp.MustCompile(`(?m)^\s*const\s+(\w+)\s*=\s*"([^"]+)"`)
	reRoute  = regexp.MustCompile(`(\w+)\.(Get|Post|Patch|Put|Delete)\(\s*([^,)]+?)\s*,([^\n]*)`)
	reMethod = regexp.MustCompile(`^(Post|Patch|Put|Delete)$`)
)

// salonScoped reports whether a full path addresses something the salon shares
// rather than something one artist owns.
func salonScoped(path string) bool {
	// /api/v1/admin/** is gated by RequireRole("admin"), a platform role that
	// has nothing to do with standing inside a salon. A capability guard
	// there would be asking the wrong question.
	if strings.Contains(path, "/admin/") {
		return false
	}
	for _, p := range []string{
		"/artists/salon/",
		"/artists/stores/",
		"/salons/",
		"/billing/",
	} {
		if strings.Contains(path, p) {
			return true
		}
	}
	return strings.HasSuffix(path, "/artists/salon")
}

// guarded reports whether a registration line applies a capability guard,
// either inline or through a local named for one.
func guarded(line string) bool {
	return strings.Contains(line, "RequireSalonCapability") ||
		strings.Contains(line, "canWrite") ||
		strings.Contains(line, "canRead")
}

type route struct {
	method, path, file string
}

func collectRoutes(t *testing.T) []route {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolving internal/: %v", err)
	}

	var out []route
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
		body := string(src)
		if !strings.Contains(body, "RegisterRoutes") {
			return nil
		}

		groups := map[string]string{}
		for _, m := range reGroup.FindAllStringSubmatch(body, -1) {
			groups[m[1]] = m[2]
		}
		consts := map[string]string{}
		for _, m := range reConst.FindAllStringSubmatch(body, -1) {
			consts[m[1]] = m[2]
		}

		rel, _ := filepath.Rel(root, path)
		for _, m := range reRoute.FindAllStringSubmatch(body, -1) {
			recv, method, rawPath, rest := m[1], m[2], m[3], m[4]
			if !reMethod.MatchString(method) {
				continue
			}
			full := resolvePath(rawPath, consts)
			if full == "" {
				continue
			}
			if prefix, ok := groups[recv]; ok {
				full = prefix + full
			} else if recv != "app" {
				continue
			}
			out = append(out, route{
				method: strings.ToUpper(method), path: full,
				file: filepath.ToSlash(rel) + "|" + rest,
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}
	return out
}

// resolvePath turns a route's first argument into a literal path, handling the
// `base+"/salon/stores"` form the artist domain uses.
func resolvePath(raw string, consts map[string]string) string {
	raw = strings.TrimSpace(raw)
	var b strings.Builder
	for _, part := range strings.Split(raw, "+") {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, `"`) && strings.HasSuffix(part, `"`):
			b.WriteString(strings.Trim(part, `"`))
		default:
			v, ok := consts[part]
			if !ok {
				return "" // a path this test cannot resolve statically
			}
			b.WriteString(v)
		}
	}
	return b.String()
}

// TestEverySalonScopedWriteIsGuarded is the build-breaker.
func TestEverySalonScopedWriteIsGuarded(t *testing.T) {
	routes := collectRoutes(t)
	if len(routes) < 20 {
		t.Fatalf("only found %d mutating routes across internal/ - the parser has "+
			"stopped matching registrations and this test has become vacuous",
			len(routes))
	}

	var unguarded []string
	seen := map[string]bool{}
	for _, r := range routes {
		if !salonScoped(r.path) {
			continue
		}
		key := r.method + " " + r.path
		seen[key] = true
		if _, ok := exemptRoutes[key]; ok {
			continue
		}
		parts := strings.SplitN(r.file, "|", 2)
		if guarded(parts[1]) {
			continue
		}
		unguarded = append(unguarded, key+"   ("+parts[0]+")")
	}

	sort.Strings(unguarded)
	if len(unguarded) > 0 {
		t.Errorf("%d salon-scoped mutating route(s) carry no capability guard:\n\n  %s\n\n"+
			"Every write to something a salon shares - services, stores, hours, "+
			"discounts, products, payment methods, billing - must be behind "+
			"middleware.RequireSalonCapability. If a route genuinely should be "+
			"open to every member, add it to exemptRoutes with a reason about "+
			"the route.",
			len(unguarded), strings.Join(unguarded, "\n  "))
	}
}

// TestExemptionsAreAllReal keeps the exemption list from outliving the routes
// it excuses. A stale entry is permission granted to nothing, and it hides
// that the exception was already resolved.
func TestExemptionsAreAllReal(t *testing.T) {
	routes := collectRoutes(t)
	live := map[string]bool{}
	for _, r := range routes {
		live[r.method+" "+r.path] = true
	}
	for key := range exemptRoutes {
		if !live[key] {
			t.Errorf("exemptRoutes names %q, which is not a registered route - "+
				"remove the entry", key)
		}
	}
}

// TestGuardsAreActuallyFound proves the parser sees the guards that exist,
// rather than passing because it matched nothing at all.
func TestGuardsAreActuallyFound(t *testing.T) {
	routes := collectRoutes(t)
	var found int
	for _, r := range routes {
		if parts := strings.SplitN(r.file, "|", 2); guarded(parts[1]) {
			found++
		}
	}
	if found < 10 {
		t.Fatalf("parser found only %d guarded routes; Phase 1 registered more "+
			"than that, so the parser is not reading registrations correctly", found)
	}
	t.Logf("%d guarded salon writes detected", found)
}
