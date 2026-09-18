-- Stores every phone number in E.164, so it can actually be messaged.
--
-- WHAT WAS WRONG
--
-- 24 of 29 user rows held bare local digits ("71900001") and 5 held some form
-- of + prefix. The notification worker builds its Twilio destination as
-- `"whatsapp:" + storedValue`, with no normalisation anywhere in between - so
-- 24 users would have been sent `whatsapp:71900001`, which is not a valid
-- destination and which Twilio rejects outright.
--
-- That is a total, silent failure of every notification to those users. It is
-- invisible today only because WhatsApp delivery is still blocked on Meta
-- business verification; the day it is unblocked, 83% of messages fail.
--
-- WHAT THIS MIGRATION DELIBERATELY DOES NOT DO
--
-- users.phone carries a partial UNIQUE index. Normalising reveals THREE
-- numbers that appear twice, because the application treated "70123456" and
-- "+96170123456" as two different people and duly created two accounts for
-- each. One of the three is only a collision among soft-deleted rows and is
-- therefore harmless; the other two are live duplicate accounts.
--
-- Merging two customer accounts means deciding which bookings, reviews and
-- deposits survive. That is a business decision with money attached, not
-- something a migration should make at 3am. So this normalises everything it
-- safely can and LEAVES the collisions exactly as they are - findable with
-- the query at the bottom of this file.
--
-- Going forward internal/pkg/phone normalises at every entry point, so no new
-- duplicate can be created this way.

-- Only rows whose canonical form is not already taken by a DIFFERENT live
-- user. The NOT EXISTS is what makes this safe to run against real data.
UPDATE users u
   SET phone = '+961' || ltrim(u.phone, '0'),
       updated_at = NOW()
 WHERE u.phone IS NOT NULL
   AND u.phone NOT LIKE '+%'
   AND u.phone ~ '^[0-9]+$'
   AND NOT EXISTS (
         SELECT 1 FROM users other
          WHERE other.id <> u.id
            AND other.deleted_at IS NULL
            AND other.phone = '+961' || ltrim(u.phone, '0')
       );

-- Same treatment for the phone snapshot on stores, which has no uniqueness
-- constraint and so cannot collide.
UPDATE stores
   SET phone = '+961' || ltrim(phone, '0'),
       updated_at = NOW()
 WHERE phone IS NOT NULL
   AND phone NOT LIKE '+%'
   AND phone ~ '^[0-9]+$';

-- Notifications hold their own recipient_phone snapshot for messages queued
-- before a users row existed. Unsent ones still need to be sendable.
UPDATE notifications
   SET recipient_phone = '+961' || ltrim(recipient_phone, '0')
 WHERE recipient_phone IS NOT NULL
   AND recipient_phone NOT LIKE '+%'
   AND recipient_phone ~ '^[0-9]+$'
   AND sent_at IS NULL;

-- WHAT IS LEFT, AND HOW TO FIND IT
--
-- Run this after the migration to list the duplicate accounts a human needs
-- to merge. It should be a handful of rows and it should shrink to zero once
-- they are resolved:
--
--   SELECT u.phone, count(*), string_agg(u.name, ' | ')
--     FROM users u
--    WHERE u.deleted_at IS NULL AND u.phone IS NOT NULL
--    GROUP BY regexp_replace(u.phone, '^\+?961|^0', '')
--   HAVING count(*) > 1;
