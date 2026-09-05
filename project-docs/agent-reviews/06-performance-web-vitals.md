# 06 — Performance & Web Vitals

**2026-09-06.** Bundle size, rendering, Core Web Vitals, asset delivery.

## Correction to the brief

The request asks about *"unnecessary re-renders, hook dependency issues, client
vs. server component boundary misplacements"*. Those are React concepts.
This is **Angular 21, client-rendered, zoneless** — the analogues are change
detection strategy, signal graph shape, and lazy-route boundaries, which is
what was audited.

There is also **no SSR** (agent 02 / decision D10: share previews are served by
a Go pre-render endpoint instead, deliberately). So there is no server/client
boundary to misplace.

---

## Measured baseline

```
customer-pwa       initial  398.31 kB raw   →   91.56 kB transfer
artist-dashboard   initial  415.50 kB raw   →   95.31 kB transfer

routes:  customer-pwa 13 lazy · artist-dashboard 23 lazy  (100% lazy)
```

**Under 100 kB transfer for the initial payload is good** — comfortably inside
the ~170 kB budget usually cited for a fast 3G first load, which matters here
because the target market is Lebanese mobile networks.

## Rendering posture is already modern

| Feature | Status |
|---|---|
| Zoneless change detection | **yes** — see below |
| `OnPush` | 51 files |
| Signals / `computed` | 43 / 28 files |
| Lazy routes | 13 + 23, all of them |
| `@defer` blocks | **0** |
| `NgOptimizedImage` | **0** |

### Zoneless is achieved by absence, which is fragile · **P2**

```
package.json          zone.js:      not a dependency
angular.json          polyfills:    None   (both apps)
app.config.ts         provideZoneChangeDetection: absent
```

Angular 21 runs zoneless when zone.js is not present. That is correct and it is
**the single biggest performance property this frontend has** — no
monkey-patched async, no whole-tree checks.

It is also invisible. Nothing declares the intent, and any of these silently
reintroduces zone.js and a global performance regression:

- a dependency that lists `zone.js` as a peer,
- someone adding `"polyfills": ["zone.js"]` to fix an unrelated build error,
- a scaffolding command that regenerates `app.config.ts`.

**Fix — make the intent explicit and load-bearing:**

```diff
 // projects/customer-pwa/src/app/app.config.ts
 export const appConfig: ApplicationConfig = {
   providers: [
+    // Explicit rather than implied. The apps run zoneless because zone.js is
+    // not a dependency, which means the property is invisible and one stray
+    // polyfills entry silently restores whole-tree change detection.
+    provideZonelessChangeDetection(),
     provideBrowserGlobalErrorListeners(),
```

---

## F1 — Images bypass every optimisation Angular offers · **P1 for LCP**

6 raw `<img>` tags, zero `NgOptimizedImage`, and portfolio images come from
**Cloudinary** — which means the transformation API is available and unused.

The LCP element on the two most-visited screens (Discover, artist profile) is a
portfolio photo. Today it is served at whatever dimensions Cloudinary stored,
with no `width`/`height`, no `srcset`, no priority hint, and therefore both a
slow LCP **and** layout shift as it arrives.

**Before:**

```html
<img [src]="artist.avatar_url" alt="" class="w-full h-48 object-cover rounded-xl" />
```

**After** — `NgOptimizedImage` with a Cloudinary loader:

```ts
// app.config.ts
import { provideImgixLoader, IMAGE_LOADER, ImageLoaderConfig } from '@angular/common';

providers: [
  {
    provide: IMAGE_LOADER,
    useValue: (config: ImageLoaderConfig) => {
      // f_auto picks AVIF/WebP per browser, q_auto picks quality per network.
      const w = config.width ? `,w_${config.width}` : '';
      return `https://res.cloudinary.com/${CLOUD}/image/upload/f_auto,q_auto,c_fill${w}/${config.src}`;
    },
  },
],
```

```html
<img ngSrc="{{ artist.cloudinary_id }}"
     width="640" height="384"
     priority                      <!-- ONLY on the LCP image, never in a list -->
     sizes="(max-width: 640px) 100vw, 640px"
     alt="{{ artist.name }}'s work"
     class="w-full h-48 object-cover rounded-xl" />
```

This single change addresses all three vitals at once:

- **LCP** — `f_auto,q_auto` typically cuts a JPEG hero by 60–80%; `priority`
  emits `fetchpriority="high"` and a preload.
- **CLS** — explicit `width`/`height` reserves the box before the bytes land.
- **INP** — fewer bytes decoding on the main thread on a mid-range Android.

`NgOptimizedImage` also *warns in dev* when `priority` is missing on the LCP
element, which turns this from a one-off fix into an enforced rule.

---

## F2 — No `@defer`, and there are three obvious candidates · **P2**

Every route is lazy, so the coarse-grained win is taken. The remaining wins are
below-the-fold work inside a route:

| Candidate | Why |
|---|---|
| `location-map` on the artist profile | ships map logic to every profile view; almost always below the fold |
| Portfolio gallery | large, below the fold, not needed for LCP |
| Notification panel body | only rendered when opened |

```html
@defer (on viewport) {
  <bedge-location-map [lat]="store.latitude!" [lng]="store.longitude!" />
} @placeholder (minimum 100ms) {
  <div class="h-40 rounded-xl bg-gray-100"></div>
}
```

`@placeholder` with a fixed height is the important half — without it, deferring
*creates* the layout shift it was meant to avoid.

---

## F3 — Lucide icons are imported one by one, which is correct · no action

`app.config.ts` imports ~30 named icons into `LucideAngularModule`. Verbose,
but it is exactly right: tree-shakeable, no sprite sheet, no runtime fetch. The
alternative (`LucideAngularModule.pick(allIcons)`) would add hundreds of
kilobytes.

Recorded so nobody "tidies" it.

---

## F4 — Polling runs every 60s and already pauses correctly · no action

The notification bell polls `/notifications/unread-count` on a 60-second
interval and **pauses on a hidden tab**. That is the right shape. Worth noting
only because it is the sole background timer in either app, so there is no
accumulating-interval problem to find.

---

## F5 — No performance budget in the build · **P2**

`angular.json` sets no `budgets`. Bundle size is good *today* and nothing
guards it. One incautious import (a date library, a chart package, a component
kit) regresses it silently and nobody notices until a user on a slow network
does.

```json
// angular.json → projects.customer-pwa.architect.build.configurations.production
"budgets": [
  { "type": "initial", "maximumWarning": "110kb", "maximumError": "150kb" },
  { "type": "anyComponentStyle", "maximumWarning": "4kb" }
]
```

Set against **transfer** size, with the warning just above today's 91.56 kB so
it fires on a real regression rather than on noise.

---

## Field data: unavailable, and that is the honest gap

Everything above is lab analysis — bundle stats, static inspection, and
reasoning about the critical path. **No Lighthouse run, no RUM, no field CWV
data exists for this project**, so no LCP/INP/CLS *number* is claimed here.

The single highest-value next step is not a code change:

1. Run Lighthouse against `/discover` and `/a/:handle` on a throttled mobile
   profile.
2. Deploy behind a CDN (agent 02 F5, agent 04 F5 both independently arrive
   here) and collect field CWV.

Until then F1 is a well-founded inference — images with no dimensions and no
compression are the LCP and CLS problem on almost every image-led PWA — not a
measurement.

---

## Recommended action

| # | Action | Priority |
|---|---|---|
| F1 | `NgOptimizedImage` + Cloudinary `f_auto,q_auto` loader | **P1** |
| — | Lighthouse baseline on `/discover` and `/a/:handle` | **P1** |
| F5 | Add bundle budgets to `angular.json` | P2 |
| Zoneless | Add `provideZonelessChangeDetection()` explicitly | P2 |
| F2 | `@defer (on viewport)` for map, gallery, notification body | P2 |
| F3/F4 | No action — icon imports and polling are already correct | — |
