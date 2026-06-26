# B-Edge — Session Commands Reference

> All `docker exec` SQL and `curl` commands from the discovery + client CRM + verification session.
> DB container: `bedge-postgres` · db=`bedge` · user=postgres · June 2026.

---

## 1. Migration state & verification

```bash
# Check current migration version + dirty flag
docker exec -i bedge-postgres psql -U postgres -d bedge -c \
  "SELECT * FROM schema_migrations;"

# Run pending migrations (from repo root, via Makefile)
make migrate

# Verify the discovery category column landed (migration 008)
docker exec -i bedge-postgres psql -U postgres -d bedge -c "\d artists" | grep -i category
```

---

## 2. Inspecting schema (used while building discovery + client)

```bash
# Describe a table
docker exec -i bedge-postgres psql -U postgres -d bedge -c "\d client_notes"
docker exec -i bedge-postgres psql -U postgres -d bedge -c "\d artists"
```

---

## 3. Seed data for discovery (Rania + Test Artist)

```bash
# List all artists with their names (to find UUIDs)
docker exec -i bedge-postgres psql -U postgres -d bedge -c \
  "SELECT a.id, u.name FROM artists a JOIN users u ON u.id = a.user_id;"

# Set Rania's primary category
docker exec -i bedge-postgres psql -U postgres -d bedge -c \
  "UPDATE artists SET category='makeup' WHERE id='378cd76e-6c75-4c63-9d38-6f8fa211f1e5';"

# Set Test Artist's category (so the filter discriminates)
docker exec -i bedge-postgres psql -U postgres -d bedge -c \
  "UPDATE artists SET category='hair' WHERE id='a38ea468-3a73-44d4-9728-da7aaec0edcc';"

# Verify a category took
docker exec -i bedge-postgres psql -U postgres -d bedge -c \
  "SELECT id, category FROM artists WHERE id='378cd76e-6c75-4c63-9d38-6f8fa211f1e5';"
```

---

## 4. Linking Rania to stores (discovery needs an active store)

```bash
# Check whether Rania has any store linked (was empty → why discovery returned [])
docker exec -i bedge-postgres psql -U postgres -d bedge -c \
  "SELECT a.id AS artist, s.id AS store, s.city, s.is_active
   FROM artists a
   LEFT JOIN artist_stores ast ON ast.artist_id = a.id
   LEFT JOIN stores s ON s.id = ast.store_id
   WHERE a.id = '378cd76e-6c75-4c63-9d38-6f8fa211f1e5';"

# List all stores (to get store UUIDs + confirm salon match)
docker exec -i bedge-postgres psql -U postgres -d bedge -c \
  "SELECT id, name, city, salon_id, is_active FROM stores;"

# Link Rania to BOTH stores (so she appears in both city sections)
docker exec -i bedge-postgres psql -U postgres -d bedge -c \
  "INSERT INTO artist_stores (artist_id, store_id) VALUES
     ('378cd76e-6c75-4c63-9d38-6f8fa211f1e5', '24869c23-b5be-48d1-a22a-08fed461010c'),
     ('378cd76e-6c75-4c63-9d38-6f8fa211f1e5', '135c6b9e-04fe-4822-8446-726bbb6c9e4a');"

# Confirm Rania's salon_id matches the stores' salon (so her services show on profile)
docker exec -i bedge-postgres psql -U postgres -d bedge -c \
  "SELECT id, salon_id FROM artists WHERE id='378cd76e-6c75-4c63-9d38-6f8fa211f1e5';"
```

---

## 5. Discovery endpoint tests (public, no auth) — VERIFIED WORKING

```bash
# makeup filter → returns ONLY Rania (twice: once per city)
curl -s "http://localhost:3000/api/v1/discovery/artists?category=makeup" | jq

# hair filter → returns ONLY Test Artist
curl -s "http://localhost:3000/api/v1/discovery/artists?category=hair" | jq

# no filter → all artists, one row per (artist, city)
curl -s "http://localhost:3000/api/v1/discovery/artists" | jq

# Rania's full public profile → artist + stores[] + services[]
curl -s "http://localhost:3000/api/v1/discovery/artists/378cd76e-6c75-4c63-9d38-6f8fa211f1e5" | jq
```

---

## 6. Swagger spec sanity checks (confirm routes are in the generated spec)

```bash
grep -c "discovery/artists"  docs/swagger.json
grep -c "clients"            docs/swagger.json
grep -c "guest/hold"         docs/swagger.json
grep -c "calendar"           docs/swagger.json
grep -c "reviews/{id}/show"  docs/swagger.json
```

---

## 7. Still-pending verification commands (for next session)

These need the server running + a Rania artist JWT.

```bash
# Find Rania's login email (need her password too, or register a fresh artist)
docker exec -i bedge-postgres psql -U postgres -d bedge -c \
  "SELECT u.id, u.email, u.role FROM users u
   JOIN artists a ON a.user_id = u.id
   WHERE a.id = '378cd76e-6c75-4c63-9d38-6f8fa211f1e5';"

# Log in to get an access token
curl -s -X POST http://localhost:3000/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"<rania-email>","password":"<her-password>"}' | jq

# --- Client CRM (Bearer = artist token) ---
# List the artist's clients (the heavy aggregate query)
curl -s "http://localhost:3000/api/v1/clients" \
  -H "Authorization: Bearer <TOKEN>" | jq

# One client's profile + history
curl -s "http://localhost:3000/api/v1/clients/<customer_id>" \
  -H "Authorization: Bearer <TOKEN>" | jq

# Upsert a private note
curl -s -X PUT "http://localhost:3000/api/v1/clients/<customer_id>/notes" \
  -H "Authorization: Bearer <TOKEN>" -H "Content-Type: application/json" \
  -d '{"content":"prefers 2pm appointments"}' | jq

# --- Review recompute check ---
# Before: note the artist's current rating
docker exec -i bedge-postgres psql -U postgres -d bedge -c \
  "SELECT rating, review_count FROM artists WHERE id='378cd76e-6c75-4c63-9d38-6f8fa211f1e5';"

# Create a review on a COMPLETED booking (customer token, not artist)
curl -s -X POST "http://localhost:3000/api/v1/reviews/" \
  -H "Authorization: Bearer <CUSTOMER_TOKEN>" -H "Content-Type: application/json" \
  -d '{"booking_id":"<completed_booking_id>","rating":5,"comment":"Amazing"}' | jq

# After: confirm rating moved off 0
docker exec -i bedge-postgres psql -U postgres -d bedge -c \
  "SELECT rating, review_count FROM artists WHERE id='378cd76e-6c75-4c63-9d38-6f8fa211f1e5';"

# --- HideReview check (should return 204, not 403) ---
curl -s -i -X PATCH "http://localhost:3000/api/v1/reviews/<review_id>/hide" \
  -H "Authorization: Bearer <ARTIST_TOKEN>"
```

---

## Key IDs (real DB)

```
Rania artist_id   378cd76e-6c75-4c63-9d38-6f8fa211f1e5   (category=makeup)
Rania salon_id    327ad1df-28dd-481a-b713-cca3bd1aaa51
Beirut Downtown   24869c23-b5be-48d1-a22a-08fed461010c   (city=Beirut)
Tripoli store     135c6b9e-04fe-4822-8446-726bbb6c9e4a   (city=Tripoli)
Test Artist       a38ea468-3a73-44d4-9728-da7aaec0edcc   (category=hair)
```

---

*B-Edge · session command log · June 2026*
