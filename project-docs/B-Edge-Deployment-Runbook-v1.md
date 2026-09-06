# B-Edge — Deployment & backup runbook

**v1, 2026-09-06.** How this goes to production, and how it comes back after a
disaster.

> **Nothing is deployed yet.** There is no Dockerfile, no CI, no host. This
> document covers the parts that are built and verified, and names the parts
> that need a decision or a purchase.

---

## 1. The shape

```
        Cloudflare (Free)
        DNS · TLS · CDN · DDoS · 5 firewall rules
                 │
                 │  CF-Connecting-IP
                 ▼
        one host (~€4/mo)
        ├── b-edge-api      (Go binary, :3000)
        └── postgres 15     (same box, unix socket / localhost)
```

**Postgres is self-hosted, deliberately.** Managed Postgres sells backups,
failover and patching, not Postgres. Only backups matter at this stage and
they are free to do properly (§4). Self-hosting also removes an extension
risk: the double-booking guard needs **`btree_gist`**, which managed providers
gate, and co-locating the database removes a network hop from the hottest path
in the app.

Revisit when downtime measured in hours stops being acceptable — that is a
volume question, not a today question.

---

## 2. Environment

The server refuses to boot without these (`internal/config/env.go`):

```
DB_HOST DB_PORT DB_NAME DB_USER DB_PASSWORD
JWT_SECRET JWT_REFRESH_SECRET        # ≥32 chars each, enforced at boot
CLIENT_URL APP_ENV
CLOUDINARY_CLOUD_NAME CLOUDINARY_API_KEY CLOUDINARY_API_SECRET
```

Three that are optional and matter in production:

| Variable | Set it to | Why |
|---|---|---|
| `TRUSTED_PROXIES` | Cloudflare's IPv4 + IPv6 ranges, comma-separated | **Without it every visitor shares one rate-limit bucket** — see §3 |
| `PROXY_HEADER` | leave unset for Cloudflare | Defaults to `CF-Connecting-IP` |
| `TWILIO_WHATSAPP_FROM` | the verified sender | Blocked on Meta business verification (**D8**) |

`CLIENT_URL` is currently `http://localhost:4200,http://localhost:4300` and
**must** become the real origins — CORS allows only what is listed.

---

## 3. The proxy step, and why it is not optional

Fiber's `c.IP()` returns the socket peer. Behind Cloudflare that is
Cloudflare, not the visitor. Three things read it:

- the **per-IP rate limiter** — all traffic would key to one bucket of 600
  requests / 5 min and the API would rate-limit itself into an outage,
- the **audit log** — every admin approval, invoice confirm and void would
  record Cloudflare's IP instead of the admin's,
- the **request log** — every line loses the only field identifying an abusive
  client.

```bash
TRUSTED_PROXIES="$(curl -s https://www.cloudflare.com/ips-v4 | paste -sd, -),$(curl -s https://www.cloudflare.com/ips-v6 | paste -sd, -)"
```

**The default is to trust nothing.** With `TRUSTED_PROXIES` unset, a forged
`CF-Connecting-IP` is ignored — verified: a request carrying
`CF-Connecting-IP: 6.6.6.6` logged `127.0.0.1`. Proxy trust is switched on
when a proxy appears, never left on by default.

Startup says which mode it is in:

```
Trusting proxy client-IP header  {"header": "CF-Connecting-IP", "trusted_cidrs": 22}
No trusted proxies configured; client IP is the socket peer
```

**Also lock the origin down.** Trusting Cloudflare's ranges is only half the
control — if the origin is reachable directly, anyone can connect from
anywhere. Firewall :3000 to Cloudflare's ranges only, or use a Cloudflare
Tunnel.

---

## 4. Backups

### Take one

```bash
BACKUP_DIR=/var/backups/bedge \
OFFSITE_CMD='rclone copy {} r2:bedge-backups/' \
  scripts/backup.sh
```

`pg_dump -Fc`, verified before it is kept: a dump under 10 KB or one
`pg_restore --list` cannot parse is deleted and the script exits non-zero.
**Rotation runs last and only after a good dump**, so a failing backup can
never delete the good ones.

**`OFFSITE_CMD` is the most important setting here.** A dying disk takes local
backups with it. Unset, the script warns; set and failing, it exits non-zero
rather than reporting success in cron. Cloudflare R2 and Backblaze B2 both
have free tiers far larger than this database.

```cron
17 3 * * *  cd /srv/b-edge-api && OFFSITE_CMD='…' scripts/backup.sh >> /var/log/bedge-backup.log 2>&1
20 3 * * 0  cd /srv/b-edge-api && scripts/restore-drill.sh   >> /var/log/bedge-drill.log  2>&1
```

### Prove it works — weekly, not annually

```bash
scripts/restore-drill.sh
```

Restores the newest dump into a scratch database and checks five things. The
last two are the point:

```
[PASS] all tables restored
[PASS] bookings table readable (31 rows)
[PASS] artists present (6)
[PASS] btree_gist and pgcrypto restored
[PASS] GIST exclusion constraint present
[PASS] exclusion constraint still REJECTS a double booking
DRILL PASSED
```

Checking that `pg_restore` exited 0 proves almost nothing. The drill inserts a
booking and then an **overlapping** one, and requires the second to be
rejected. A restore that quietly lost `btree_gist` would produce a database
that looks complete and has silently lost the only thing preventing two
customers being booked into one slot.

The scratch database is dropped on exit, pass or fail.

### What this does NOT give you

**No point-in-time recovery.** A nightly dump means losing up to 24 hours of
bookings in a disaster. That is accepted at one launch artist and stops being
acceptable at real volume — at which point the answer is WAL archiving
(pgBackRest, wal-g), not a tighter cron.

Stated here rather than discovered during an incident.

---

## 5. Restoring for real

```bash
# 1. stop the API so nothing writes while you work
systemctl stop bedge-api

# 2. keep the corrupted database rather than dropping it — it may hold rows
#    the backup does not, and you get exactly one chance to preserve it
psql -U postgres -c "ALTER DATABASE bedge RENAME TO bedge_broken_$(date +%s);"
psql -U postgres -c "CREATE DATABASE bedge;"

# 3. restore
pg_restore --no-owner -U postgres -d bedge /var/backups/bedge/bedge-….dump

# 4. verify before letting traffic back in
psql -U postgres -d bedge -c "
  SELECT count(*) FROM bookings;
  SELECT count(*) FROM pg_constraint WHERE conname='bookings_artist_id_tstzrange_excl';"

# 5. migrations are idempotent; run them in case the dump predates a deploy
go run ./cmd/migrate up

systemctl start bedge-api
```

Step 2 is the one people skip. Renaming instead of dropping costs disk and
buys the ability to recover rows written after the last backup.

---

## 6. Caching

Public reads are CDN-cacheable; everything else is `no-store` **by default**
(`internal/middleware/secheaders.go`), with the public reads opting back in
(`internal/pkg/httpcache`). Caching by default and opting out for private data
has one failure mode and it is a data breach.

| Surface | Shared-cache window |
|---|---|
| `/discovery/artists`, `/discovery/artists/:id` | 60s |
| `/artists/:id`, `/…/services`, `/…/stores`, `/media/portfolio/:id` | 5 min |
| `/public/reviews/artist/:id` | 2 min |
| `/a/:handle` (crawler preview) | 10 min |
| everything else, including `/c/:token` | `no-store` |

**Named trade-off:** `open_status` is derived per request, so a 60-second
window means a store can show "Open now" for up to a minute after closing.
Accepted, and the reason the discovery window is 60s rather than 5 min. The
badge is advisory — booking goes through slot generation, which is never
cached and will refuse a closed store.

---

## 7. Still outstanding

| Item | Blocked on |
|---|---|
| Domain purchase | you — also unblocks Meta verification |
| Host + Cloudflare account | you |
| Dockerfile / CI | choice of host |
| `TWILIO_WHATSAPP_FROM` | Meta business verification (**D8**) |
| WAF managed rules | Cloudflare Pro, worth it at launch not before |
