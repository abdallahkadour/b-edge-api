-- Strips the Lebanese country code back off, returning to bare local digits.
-- Numbers from other MENA countries are NOT touched: there is no bare local
-- form to return them to, and guessing one would corrupt them.
UPDATE users        SET phone = substring(phone from 5)
 WHERE phone LIKE '+961%';
UPDATE stores       SET phone = substring(phone from 5)
 WHERE phone LIKE '+961%';
UPDATE notifications SET recipient_phone = substring(recipient_phone from 5)
 WHERE recipient_phone LIKE '+961%' AND sent_at IS NULL;
