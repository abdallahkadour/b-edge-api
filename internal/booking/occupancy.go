package booking

// Interval algebra for scheduling.
//
// Today a booking is one contiguous range and this type is barely more than
// a slice. It exists now, before it is needed, because three planned
// features — service buffers, physical resources, and split bookings with
// processing gaps — all modify the same calculation, and the feasibility
// assessment §4 names doing them one at a time as an architectural trap:
//
//	"If two or more of these are genuinely on the roadmap, model the
//	 interval algebra once, deliberately, before building any of them."
//
// The design decisions behind it are in
// B-Edge-Interval-Algebra-Decision-v1.md. Two are load-bearing enough to
// repeat here:
//
//   - Intervals are HALF-OPEN, [start, end). Two intervals that merely touch
//     do not overlap. This is what makes back-to-back bookings possible and
//     it matches the `tstzrange(start_time, end_time, '[)')` in the GIST
//     exclusion constraint, so the application and the database agree by
//     construction rather than by coincidence.
//
//   - Every interval carries a KIND. Today everything blocks the artist and
//     the kind is only documentation. It stops being documentation the
//     moment resources arrive: a chair being occupied blocks the chair, not
//     the artist, and an untyped set cannot express that difference.

import (
	"sort"
	"time"
)

// IntervalKind says what an occupied span represents, and therefore what it
// blocks.
type IntervalKind string

const (
	// KindService is the appointment itself - the customer is present and
	// the artist is working.
	KindService IntervalKind = "service"

	// KindTravel is time an artist needs to move between stores. Attached
	// to a booking at a DIFFERENT store than the one being queried; see
	// TravelIntervals.
	KindTravel IntervalKind = "travel"

	// KindBuffer is cleanup or turnaround after a service. Not yet
	// produced by anything - Sprint 6 adds `services.buffer_min`. Declared
	// now so the type it will need already exists and its meaning is
	// settled: the artist is unavailable, the customer is not present, and
	// it can be released early when a booking completes ahead of time.
	KindBuffer IntervalKind = "buffer"

	// KindProcessingGap is time inside a service when the artist is free
	// but the customer and the chair are not - colour developing, keratin
	// setting. Not yet produced by anything; `services.active_duration_min`
	// exists in the schema and is read by nothing. This is the one kind
	// that does NOT block the artist, which is precisely why the set has to
	// be typed before split bookings are built rather than after.
	KindProcessingGap IntervalKind = "processing_gap"
)

// BlocksArtist reports whether an interval of this kind makes the artist
// unavailable.
//
// The whole reason kinds exist. Everything blocks the artist today; a
// processing gap will not.
func (k IntervalKind) BlocksArtist() bool {
	return k != KindProcessingGap
}

// Interval is a half-open span of time, [Start, End), with a kind.
type Interval struct {
	Start time.Time
	End   time.Time
	Kind  IntervalKind
}

// IsEmpty reports whether the interval covers no time at all.
//
// Zero-length and inverted intervals are treated the same way - as nothing -
// rather than being rejected. A buffer of zero minutes is a legitimate
// configuration (it is the default), and an inverted interval can only come
// from a caller bug, where silently occupying no time is a far better
// failure than blocking a whole day.
func (iv Interval) IsEmpty() bool {
	return !iv.End.After(iv.Start)
}

// Overlaps reports whether two intervals share any time.
//
// Half-open: [10:00, 11:00) and [11:00, 12:00) do NOT overlap. Getting this
// wrong makes back-to-back appointments impossible, which is why
// slots_golden_test.go pins it explicitly.
func (iv Interval) Overlaps(other Interval) bool {
	if iv.IsEmpty() || other.IsEmpty() {
		return false
	}
	return iv.Start.Before(other.End) && other.Start.Before(iv.End)
}

// Occupancy is a set of intervals describing when someone or something is
// unavailable.
//
// Deliberately not kept sorted or merged on insert. Slot generation asks
// "does this candidate overlap anything?", which does not care about order,
// and merging on every Add would be work done for a question nobody asks.
// Merged() exists for the callers that do care.
type Occupancy struct {
	intervals []Interval
}

// NewOccupancy builds a set from intervals, dropping empty ones.
func NewOccupancy(intervals ...Interval) *Occupancy {
	o := &Occupancy{}
	for _, iv := range intervals {
		o.Add(iv)
	}
	return o
}

// Add puts an interval into the set. Empty intervals are ignored rather than
// stored, so callers never have to guard a zero-minute buffer.
func (o *Occupancy) Add(iv Interval) {
	if iv.IsEmpty() {
		return
	}
	o.intervals = append(o.intervals, iv)
}

// AddRange is Add for a bare start/end, for callers that have no kind to
// give. Everything blocks the artist today, so `service` is the honest
// default rather than a placeholder.
func (o *Occupancy) AddRange(start, end time.Time, kind IntervalKind) {
	o.Add(Interval{Start: start, End: end, Kind: kind})
}

// Len returns how many intervals are stored. Empty ones were never added, so
// this is the count of real occupied spans.
func (o *Occupancy) Len() int { return len(o.intervals) }

// Intervals returns a copy of the set.
//
// A copy, not the slice: handing out the backing array would let a caller
// mutate occupancy through an accessor named like a getter.
func (o *Occupancy) Intervals() []Interval {
	out := make([]Interval, len(o.intervals))
	copy(out, o.intervals)
	return out
}

// BlocksArtist reports whether the candidate span collides with anything in
// this set that makes the artist unavailable.
//
// This is the question slot generation actually asks. Kinds that do not
// block the artist - a processing gap - are skipped, which is how a haircut
// will one day be bookable inside someone else's colour development.
func (o *Occupancy) BlocksArtist(candidate Interval) bool {
	if candidate.IsEmpty() {
		return false
	}
	for _, iv := range o.intervals {
		if !iv.Kind.BlocksArtist() {
			continue
		}
		if iv.Overlaps(candidate) {
			return true
		}
	}
	return false
}

// OverlapsAny reports a collision with ANY interval regardless of kind.
//
// Distinct from BlocksArtist on purpose: "is the room in use" and "is the
// artist busy" become different questions the moment resources exist, and
// having one method serve both would quietly answer the wrong one.
func (o *Occupancy) OverlapsAny(candidate Interval) bool {
	if candidate.IsEmpty() {
		return false
	}
	for _, iv := range o.intervals {
		if iv.Overlaps(candidate) {
			return true
		}
	}
	return false
}

// Merged returns the artist-blocking spans coalesced into the fewest
// possible intervals, sorted by start.
//
// Touching intervals ARE merged: [10:00,11:00) and [11:00,12:00) become
// [10:00,12:00). That is correct for describing a continuous busy stretch,
// and it is exactly why merging must never be applied before an overlap
// test - a candidate ending at 11:00 does not overlap either original but
// does overlap the merge. Merged() is for display and for reasoning about
// gaps; BlocksArtist is for decisions.
//
// The returned kind is KindService: a merged span no longer represents one
// thing, and claiming otherwise would be a lie the type system cannot catch.
func (o *Occupancy) Merged() []Interval {
	blocking := make([]Interval, 0, len(o.intervals))
	for _, iv := range o.intervals {
		if iv.Kind.BlocksArtist() {
			blocking = append(blocking, iv)
		}
	}
	if len(blocking) == 0 {
		return nil
	}

	sort.Slice(blocking, func(i, j int) bool {
		return blocking[i].Start.Before(blocking[j].Start)
	})

	out := []Interval{{Start: blocking[0].Start, End: blocking[0].End, Kind: KindService}}
	for _, iv := range blocking[1:] {
		last := &out[len(out)-1]
		// Touching counts as continuous here, hence !Before rather than
		// Before: an interval starting exactly when the last one ended
		// extends it instead of starting a new span.
		if !iv.Start.After(last.End) {
			if iv.End.After(last.End) {
				last.End = iv.End
			}
			continue
		}
		out = append(out, Interval{Start: iv.Start, End: iv.End, Kind: KindService})
	}
	return out
}

// FreeWithin returns the gaps left inside [from, to) once every
// artist-blocking interval is removed.
//
// Not used by slot generation, which walks a fixed 15-minute grid rather
// than subdividing free space. It exists because "where are the gaps" is the
// question processing gaps and resource scheduling will both ask, and
// answering it here keeps that logic out of two future call sites.
func (o *Occupancy) FreeWithin(from, to time.Time) []Interval {
	window := Interval{Start: from, End: to, Kind: KindService}
	if window.IsEmpty() {
		return nil
	}

	var free []Interval
	cursor := from
	for _, busy := range o.Merged() {
		if !busy.End.After(cursor) {
			continue // entirely before the window
		}
		if !busy.Start.Before(to) {
			break // entirely after it; Merged is sorted
		}
		if busy.Start.After(cursor) {
			free = append(free, Interval{Start: cursor, End: busy.Start, Kind: KindService})
		}
		if busy.End.After(cursor) {
			cursor = busy.End
		}
	}
	if cursor.Before(to) {
		free = append(free, Interval{Start: cursor, End: to, Kind: KindService})
	}
	return free
}

// TravelIntervals returns the time an artist is unavailable at one store
// because of a booking at a different one: a span before it to get there,
// and a span after it to come back.
//
// The booking's own duration is deliberately NOT included.
// GetArtistBookingsForDate has no store filter, so the booking is already in
// the artist's occupied set through the normal path; adding it again here
// would be harmless but would hide that coupling. It is stated instead - see
// the comment at step 4 of GetAvailableSlots.
//
// A zero or negative buffer yields empty intervals, which Occupancy.Add
// drops. Zero is the default configuration, so callers never need to guard
// it.
func TravelIntervals(bookingStart, bookingEnd time.Time, buffer time.Duration) []Interval {
	if buffer <= 0 {
		return nil
	}
	return []Interval{
		{Start: bookingStart.Add(-buffer), End: bookingStart, Kind: KindTravel},
		{Start: bookingEnd, End: bookingEnd.Add(buffer), Kind: KindTravel},
	}
}
