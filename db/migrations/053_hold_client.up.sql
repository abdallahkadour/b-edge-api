-- 053: a guest hold remembers which network made it, so one network cannot
-- hoard an artist's day.
--
-- WHY
--
-- A guest hold blocks its slot for 10 minutes and needs no account, so the
-- only thing limiting how many one person could take was the general rate
-- limit (600 requests per 5 minutes per address). Nothing stopped someone
-- from holding every slot an artist has on a Saturday, rolling, for as long
-- as they liked. Found checking an external review, 2026-09-26.
--
-- The founder's rule: at most 2 unfinished holds on the SAME artist from one
-- network address at a time. Per artist, because Lebanese carriers put many
-- phones behind one address: two strangers on the same carrier address
-- rarely hold the same artist in the same 10 minutes, so real customers are
-- almost never refused, while a hoarder cannot take a whole day.
--
-- WHAT IS STORED
--
-- hold_client is an HMAC of the address keyed with a server secret
-- (booking.holdClientKey), never the address itself: a plain hash of an IPv4
-- address can be reversed by trying all four billion. It is set only on a
-- guest hold and cleared the moment that hold ends - submitted, released or
-- expired - so a booking row never carries it once it is a booking.
--
-- The count and the insert run in one transaction under an advisory lock on
-- (artist, client) - booking.CreateGuestHold - so a burst of simultaneous
-- requests cannot all read a count below the limit.

ALTER TABLE bookings ADD COLUMN hold_client TEXT;

-- The limit's count: this artist's unfinished holds from this client.
CREATE INDEX idx_bookings_active_hold_client
  ON bookings (artist_id, hold_client)
  WHERE status = 'held';
