# 05 — Design System & Modern UI/UX

**2026-09-06.** Component architecture, design tokens, accessibility, theming.

## Correction to the brief, stated first

The review request named **shadcn/ui, Radix Primitives, React Aria, Base UI**
and asked about *"client vs. server component boundaries"* and *"hook
dependency issues"*.

**None of those exist in this project and none can be adopted.** B-Edge's
frontend is:

```
@angular/core     21.2.0        @angular/cdk    21.2.14
tailwindcss        3.4.19        lucide-angular  1.0.0
react / next / radix / shadcn:   NOT PRESENT, and not installable —
                                 they are React-only.
```

Recommending them would mean rewriting two PWAs in a different framework.
The Angular-native equivalents are used instead, and they are close analogues:

| Named in brief | Angular equivalent | Already a dependency? |
|---|---|---|
| Radix Primitives / React Aria | **`@angular/cdk`** (a11y, overlay, dialog, listbox) | **yes**, used in 9 files |
| shadcn/ui | copy-in components in `@bedge/shared/ui` | **yes**, the pattern exists |
| Tailwind CSS | Tailwind 3.4 | **yes** |
| Server components | not applicable — this is a client-rendered PWA | — |

---

## The premise "messy styling, fragmented components" is half wrong

There **is** a real design token system. `tailwind.config.js` defines semantic
scales, not raw hex scattered through templates:

```js
ink:     { DEFAULT: '#0a0a0a', 900, 800, 700 },   // "near-black, not pure #000"
gray:    { 50 … 900 },                            // "the workhorse of the whole UI"
success: { DEFAULT: '#16a34a', light, dark },     // "the one warm functional color"
danger:  { DEFAULT: '#dc2626', light, dark },
warning: { DEFAULT: '#d97706', light, dark },
```

That is a considered palette with documented intent. The problems are
**adoption** and **theming**, not the absence of a system.

---

## F1 — Component adoption is 50–77%, and the gap is measurable · **P1**

```
<bedge-button>      89   vs   raw <button>            150      → 37% bypass
bedgeInput          82   vs   raw <input|textarea>    106      → 77% adoption
<bedge-badge>       21   vs   rounded-full pills      147      → many bespoke
```

Some raw `<button>`s are legitimate (icon-only nav, star pickers) and many
`rounded-full`s are avatars or status dots — the numbers are an upper bound,
not 147 rogue badges. But the direction is clear, and the project's own E2E
plan already flagged one instance:

> *`discover.page.html:90-130` has hand-rolled pills — replace them in the same
> pass rather than adding a seventh style.*

**The shared library is only 5 components** — badge, button, card, input
directive, location-map — for **38 routes**. That is the actual gap.

### Recommended additions to `@bedge/shared/ui`

Each replaces a pattern currently hand-rolled in 3+ places:

| Component | Replaces | Built on |
|---|---|---|
| `bedge-star-rating` | 4 bespoke star loops (review form ×2, review list ×2) | — |
| `bedge-sheet` | hand-rolled bottom sheets / dropdowns | `@angular/cdk/overlay` |
| `bedge-empty-state` | ~9 duplicated "no results" blocks | — |
| `bedge-skeleton` | `animate-pulse` divs repeated across pages | — |
| `bedge-field` | label + control + error triplet, hand-repeated | wraps `bedgeInput` |

### Before / after — the star rating

Currently duplicated four times with different sizes, weights and fill classes.
The Sprint 8 dual-layer review work made this concrete: the same loop now
appears twice in one template.

**Before** (`reviews.page.html`, and three near-copies elsewhere):

```html
<div class="flex items-center gap-0.5">
  @for (filled of starsFor(r.rating); track $index) {
    <lucide-icon name="star" [size]="14" [strokeWidth]="0"
      [class]="filled ? 'text-ink fill-ink' : 'text-gray-200 fill-gray-200'" />
  }
</div>
```

**After** — one component, `readonly` and interactive modes:

```ts
// projects/shared/src/lib/ui/star-rating.component.ts
@Component({
  selector: 'bedge-star-rating',
  standalone: true,
  imports: [LucideAngularModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="flex items-center" [class.gap-0.5]="!interactive()" [class.gap-2]="interactive()"
         [attr.role]="interactive() ? 'radiogroup' : 'img'"
         [attr.aria-label]="interactive() ? label() : label() + ': ' + value() + ' of 5'">
      @for (star of stars; track star) {
        @if (interactive()) {
          <button type="button" class="p-1 active:scale-125 transition-transform"
                  role="radio" [attr.aria-checked]="value() === star"
                  [attr.aria-label]="star + ' star' + (star > 1 ? 's' : '')"
                  (click)="pick.emit(star)">
            <lucide-icon name="star" [size]="size()" [strokeWidth]="2"
                         [class]="value() >= star ? onClass() : offClass()" />
          </button>
        } @else {
          <lucide-icon name="star" [size]="size()" [strokeWidth]="0"
                       [class]="value() >= star ? onClass() : offClass()" />
        }
      }
    </div>`,
})
export class StarRatingComponent {
  readonly value = input.required<number>();
  readonly size = input(14);
  /** 'primary' rates the specialist, 'muted' rates the venue — the two must
   *  stay visually distinct so disagreement reads as informative rather than
   *  broken. See B-Edge-Review-Attribution-Spec-v1.md §2.2. */
  readonly tone = input<'primary' | 'muted'>('primary');
  readonly interactive = input(false);
  readonly label = input('Rating');
  readonly pick = output<number>();

  protected readonly stars = [1, 2, 3, 4, 5];
  protected readonly onClass = computed(() =>
    this.tone() === 'primary' ? 'text-ink fill-ink' : 'text-gray-500 fill-gray-500');
  protected readonly offClass = computed(() =>
    this.interactive() ? 'stroke-gray-300 fill-none' : 'text-gray-200 fill-gray-200');
}
```

**Call site becomes:**

```html
<bedge-star-rating [value]="r.rating" tone="primary" label="Artist" />
@if (r.salon_rating) {
  <bedge-star-rating [value]="r.salon_rating!" [size]="12" tone="muted" label="Salon" />
}
```

This also fixes an accessibility gap the current markup has: four star loops,
none of them exposing a `role` or an accessible value.

---

## F2 — No dark mode at all · **P1**

```
tailwind.config.js  darkMode:      not configured
dark: variants in templates:       0
```

A mobile-first beauty PWA in 2026 with no dark mode is a real product gap —
iOS and Android both surface a system-wide preference, and a white-only app is
conspicuous at night, which is when a lot of booking browsing happens.

The palette makes this cheap to fix **because it is already semantic**. The
work is not re-picking colours; it is moving them behind CSS custom properties
so one set of class names resolves per theme.

**Before** — colours resolve to one fixed value:

```js
// tailwind.config.js
colors: {
  ink:  { DEFAULT: '#0a0a0a', 900: '#0a0a0a', 800: '#1a1a1a' },
  gray: { 50: '#fafafa', … 900: '#18181b' },
}
```

**After** — tokens indirect through variables, and the whole app themes at once:

```css
/* styles.css */
:root {
  --surface:        255 255 255;   /* rgb triplets, so Tailwind's / opacity works */
  --surface-raised: 250 250 250;
  --border:         228 228 231;
  --text:            10  10  10;
  --text-muted:     113 113 122;
  --accent:          10  10  10;
}

:root[data-theme='dark'] {
  --surface:         10  10  10;
  --surface-raised:  26  26  26;
  --border:          42  42  46;
  --text:           250 250 250;
  --text-muted:     161 161 170;
  --accent:         250 250 250;
}

/* System default when the user has expressed no preference. */
@media (prefers-color-scheme: dark) {
  :root:not([data-theme='light']) { /* same block as above */ }
}
```

```js
// tailwind.config.js
darkMode: ['class', '[data-theme="dark"]'],
theme: { extend: { colors: {
  surface:       'rgb(var(--surface) / <alpha-value>)',
  'surface-raised': 'rgb(var(--surface-raised) / <alpha-value>)',
  border:        'rgb(var(--border) / <alpha-value>)',
  ink:           'rgb(var(--text) / <alpha-value>)',
  muted:         'rgb(var(--text-muted) / <alpha-value>)',
  // success / danger / warning keep literal values — semantic colour must
  // not invert with theme, only adjust for contrast.
}}}
```

**Sequencing note:** this is a large mechanical diff (`bg-white` → `bg-surface`,
`text-gray-400` → `text-muted`, ~38 templates). It should be one commit,
reviewed as a rename, and it should **not** be bundled with F1 — mixing a
palette migration with component extraction makes both unreviewable.

The three-state rule matters: explicit choice, explicit opposite, and *no
stamp at all* for system default. A design that only handles
`prefers-color-scheme` cannot honour an in-app toggle, and one that only
handles the toggle ignores the OS.

---

## F3 — `@angular/cdk` is a dependency and is under-used · **P2**

Present in `package.json`, imported in 9 files. Meanwhile the codebase
hand-rolls behaviours the CDK already ships correctly:

- **Overlays / dropdowns** — the notification panel manually binds Escape at
  the document and manages focus. `@angular/cdk/overlay` +
  `cdk-overlay-transparent-backdrop` handles outside-click, Escape, scroll
  strategy and positioning. (A prior session already removed a
  `cdkTrapFocus` that fought the panel — the fix was correct, but the
  replacement was hand-rolled rather than `OverlayModule`.)
- **`LiveAnnouncer`** — 0 uses. Toasts, "review submitted", and rate-limit
  banners currently announce nothing to a screen reader.
- **`cdk-listbox`** — the service picker and time-slot grid are `<button>`
  grids with no roving tabindex or arrow-key navigation.

87 `aria-*` attributes across 38 routes is thin but not absent; the gap is
*keyboard behaviour*, which attributes alone do not provide.

---

## F4 — Micro-interactions are present but ad hoc · **P3**

`active:scale-125` on stars, `animate-pulse` skeletons, `transition-opacity` on
buttons — real touches, applied inconsistently. Once F1 lands, these belong on
the components rather than at call sites.

**One accessibility gap to fix while doing it:** no `prefers-reduced-motion`
handling anywhere.

```css
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    animation-duration: 0.01ms !important;
    transition-duration: 0.01ms !important;
  }
}
```

---

## Recommended action

| # | Action | Priority |
|---|---|---|
| F2 | Tokenise colours behind CSS variables; ship dark mode | **P1** |
| F1 | Add 5 components to `@bedge/shared/ui`; migrate call sites | **P1** |
| F3 | Adopt `cdk/overlay` for the notification panel; add `LiveAnnouncer` | P2 |
| F4 | Move micro-interactions onto components; add reduced-motion | P3 |

**Not recommended:** adopting a third-party Angular component library
(Material, PrimeNG, Spartan). The bespoke components are small, on-brand, and
already carry the project's visual identity; swapping them for Material would
cost more bundle than the entire current initial payload (see agent 06) and
force a visual redesign nobody asked for.
