-- 014_dedupe_users_by_phone.down.sql
--
-- Only reverses the reversible part - the unique index. The data merge
-- itself (repointing bookings/reviews/client_notes and soft-deleting
-- duplicate user rows) is NOT undone: there is no record of which specific
-- rows a merge came from once merged, so "unmerging" would mean guessing,
-- not reversing. If this migration needs rolling back, treat it as a
-- one-way door on the data itself.

DROP INDEX IF EXISTS users_phone_unique;
