# B-Edge — Share preview rendering: SSR vs. server-side pre-render

> Written 2026-08-31. Decides how B-Edge serves Open Graph metadata for
> shared artist links (Sprint 10, T10.1 of the feasibility implementation
> plan). Recorded here rather than in a PR description because it is a
> workspace-shape decision with a permanent cost, not a code detail.

## The problem, verified

Both PWAs are client-rendered. WhatsApp, Instagram and Facebook crawlers do
not execute JavaScript, so any `<meta property="og:...">` tag written at
runtime by Angular is invisible to them.

Confirmed against the repo on 2026-08-31:

- `package.json` contains no `@angular/ssr` and no `platform-server`.
- `angular.json` has only `@angular/build:dev-server` builders — no server
  build target for any of the three projects.
- `projects/customer-pwa/src/index.html` contains exactly three meta tags:
  `charset`, `viewport`, and `theme-color`. **There is no `og:` or
  `twitter:` tag anywhere.**

So every link an artist shares today — the exact links Rania's Instagram
audience follows — renders as a bare URL with no title, no description and
no image.

## Why this matters more here than it usually would

`B-Edge-Feature-Feasibility-Assessment-v1.md` §3 flags this as possibly
outranking everything else in Phase 2, and the reasoning is specific to this
business rather than general SEO good practice: Instagram is the primary
distribution channel, the launch partner has ~300K followers, and the
booking funnel is explicitly designed around guests arriving from a shared
link with no account. The share preview is the first thing a prospective
customer sees, and right now it is nothing.

## Options considered

### A. Angular SSR (`@angular/ssr`)

Render the customer PWA on the server so crawlers receive complete HTML.

- Correct in the general case, and would also help any future SEO work.
- But: adds a server build target, a Node server process, and an SSR-safe
  constraint on every existing component (no bare `window`/`document`
  access, transfer-state handling for the initial fetch) across a workspace
  of three projects sharing one library.
- The cost is permanent and paid on every build and deploy, forever.
- It would be serving **two URL shapes to non-human clients**.

### B. Server-side pre-render from the Go API (**chosen**)

A Fiber route that returns a small static HTML document containing the OG
tags, and sends human visitors on to the PWA.

- Touches nothing in the Angular workspace; cannot regress the PWA build.
- Lives beside the data it needs — the artist profile query already exists
  in `internal/discovery`.
- Total surface: one handler, one query, one HTML template.
- Matches the existing architecture, where the Go API is already the only
  public server and the PWAs are static bundles.

### C. Static per-artist HTML at deploy time

Rejected outright: artist profiles change (name, bio, cover photo, category)
and a deploy-time snapshot would serve stale previews until the next
deploy, with no way to invalidate one.

## Decision

**Option B.** A Go endpoint at `GET /a/:handle`.

The deciding argument is proportionality. SSR is a workspace-wide
architectural commitment with a permanent build and deploy cost, adopted to
serve two URL shapes to machines that only want four meta tags. A single
handler achieves the same visible outcome and can be deleted just as easily
if SSR is later wanted for real reasons — for instance, genuine SEO on a
public marketplace, which is not currently on the roadmap.

Revisit if either becomes true:

1. Search-engine indexing of artist profiles becomes a growth channel, not
   just link previews.
2. More than a handful of URL shapes need crawlable metadata.

## Shape of the implementation

- `GET /a/:handle` accepts a handle (migration 012) or a UUID, resolving via
  the same path `GET /artists/:id` already uses, so a link works either way.
- Crawlers get HTML with `og:title`, `og:description`, `og:image` and the
  `twitter:` equivalents.
- Humans get the same document, which immediately redirects to the booking
  funnel. Serving one document to both avoids user-agent sniffing deciding
  who sees what — the crawler simply never runs the redirect, because it
  does not execute scripts and does not follow a `refresh`.
- The OG image is the artist's cover portfolio photo, resized to the 1200×630
  card format by inserting a Cloudinary transformation into the URL. No image
  generation service, no new dependency, no stored derivative.
- Artists with no photo fall back to a static branded image; the tags are
  still emitted so the card has a title and description.
- Only publicly visible artists are served, reusing the same subscription
  visibility condition as Discover — a suspended artist's link must not
  render a rich preview when their profile itself is hidden.

## What this does NOT cover

**Production routing (T10.4).** The static bundle and the API are separate
in deployment. Whatever sits in front (Nginx, per `B-Edge-INFRA.docx`) must
route `/a/*` to the Go API rather than to the PWA bundle. This is where the
feature silently fails in production while working perfectly on localhost —
the preview simply stays blank and nothing errors.

**The shared-link URL in the apps.** The funnel currently produces
`/book/:artistId`. Whether the UI should start emitting `/a/:handle` as the
canonical share URL is a separate change, deliberately not bundled here.
Both keep working either way.
