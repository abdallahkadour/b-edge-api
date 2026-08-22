DELETE FROM media WHERE owner_type = 'product';
ALTER TABLE media DROP CONSTRAINT media_owner_type_check;
ALTER TABLE media ADD CONSTRAINT media_owner_type_check
  CHECK (owner_type IN ('artist', 'salon', 'booking'));
