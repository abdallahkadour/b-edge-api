-- 056: a customer can keep a list of the artists she likes.
--
-- WHY
--
-- The PRD's launch list asked for customer profiles with "history,
-- favourites, refund status"; history and refund status shipped, favourites
-- never did. Without it, a customer who found an artist she liked has to
-- search again, or keep the link herself.
--
-- WHAT IS STORED, AND WHAT IS NOT
--
-- Only the pair and when it was made. Whether the artist can be SHOWN is not
-- stored here: the favourites list reads through discovery's artist-card
-- query, so an artist whose subscription lapses or who is suspended drops
-- out of the list by the same rule that drops her from Discover, and comes
-- back when that rule lets her - nothing here to keep in step.
--
-- Either side being deleted takes the row with it: a favourite of a deleted
-- account means nothing.

CREATE TABLE IF NOT EXISTS customer_favourite_artists (
  customer_id UUID        NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
  artist_id   UUID        NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (customer_id, artist_id)
);
