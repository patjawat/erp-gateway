---
name: "ERP Mobile — Gateway Admin"
description: "Thai operational workspace with Mobile's indigo/gold palette and modal forms."
colors:
  bg: "#f5f6fd"
  header: "#1c326f"
  nav: "#e8edf8"
  panel: "#ffffff"
  soft: "#e8edf8"
  text: "#1c2435"
  muted: "#596478"
  accent: "#1c326f"
  on-accent: "#ffffff"
  gold: "#e9b44c"
  on-gold: "#12224e"
  gold-soft: "#fff3d9"
  gold-ink: "#946415"
  border: "#d7dfeb"
  success: "#036837"
  warning: "#946415"
  danger: "#b3261e"
  dark-bg: "#101722"
  dark-header: "#12224e"
  dark-nav: "#202d42"
  dark-panel: "#192334"
  dark-soft: "#263856"
  dark-text: "#f1f4fa"
  dark-muted: "#b9c5d8"
  dark-accent: "#b6c9f5"
  dark-on-accent: "#132750"
  dark-gold: "#e9b44c"
  dark-gold-soft: "#3f321c"
  dark-gold-ink: "#e9b44c"
  dark-border: "#3f4e66"
  dark-success: "#8ad5b4"
  dark-warning: "#e9b44c"
  dark-danger: "#ffb4ab"
typography:
  title: {fontFamily: "Sarabun, sans-serif", fontWeight: 600}
  body: {fontFamily: "Sarabun, sans-serif", fontSize: "14px", fontWeight: 400}
  support: {fontFamily: "Sarabun, sans-serif", fontSize: "12px", fontWeight: 400}
rounded:
  field: "8px"
  button: "8px"
  panel: "13px"
---

# Gateway design system

## Selected direction

The user replaced the previous C design and selected **indigo and gold matching Mobile**, retaining **modal popup forms**, light/dark modes and Thai-first operation. The initial Amber palette and old C layout are historical references.

Gateway serves one central administrator. Brand lives in the solid indigo header, gold identity mark, quiet status treatment and Sarabun type. Clear actions and readable real data take priority over decoration.

The interface source of truth is:

- `frontend/src/styles.css`: paired theme variables, layout and component styles.
- `frontend/src/app/app.html` and `app.ts`: authentication and application shell.
- `frontend/src/app/routes.ts`: lazy-loaded pages under `/admin/`.
- `frontend/src/app/core/modal.component.ts`: modal lifecycle and focus behavior.
- `frontend/src/app/pages/`: registry, overview/usage, certificates, history and settings.
- `../mobile/lib/app_colors.dart`: authoritative shared brand values.

First-run/recovery setup remains in `backend/internal/admin/assets/`, served through bootstrap separately from the authenticated Angular app. Generated Angular assets in `backend/internal/admin/assets/ui/` are never edited directly.

## Color roles

**Indigo leads.** Primary actions, active navigation, links and focus use `accent` with `on-accent`. Light appearance uses `#1C326F` with white; dark uses `#B6C9F5` with `#132750`. The solid header keeps white headings and pale `#E8EDF8` supporting text.

**Gold is deliberate.** `#E9B44C` supplies identity and selected secondary emphasis. Filled gold uses deep indigo `#12224E` content. Gold text/icons on light surfaces use `#946415` over `#FFF3D9`; dark uses bright gold over `#3F321C`. Do not put white body text on gold.

**Surfaces stay quiet.** Light panels sit on `#F5F6FD`; dark panels use `#192334` over `#101722`. Text, muted text and borders use explicit paired roles. Never invert the light theme mechanically. Success, warning and error always carry wording or icons alongside color.

Default appearance is light. Switching mode changes the page and open modal. Persist the appearance preference only; credentials, keys and form drafts never become theme settings. Design-comparison controls belong to previews, not the production administration flow.

## Typography and layout

Self-host Sarabun Regular/SemiBold from `frontend/public/fonts/`. Supporting copy stays at least 12px. Use tabular numerals for aligned counts and dates. No external font service is needed.

The shell has a horizontal header/navigation and a bounded centered workspace. Narrow screens wrap controls and stack working regions. Navigation and wide tables have their own overflow containers so the document does not scroll horizontally. Forms use two columns when space permits and one on phones.

Panels have quiet borders and gently rounded corners. Reserve overlay depth for dialogs. Use Lucide icons with text or an accessible name, retain visible focus and respect reduced motion. No decorative hero or invented activity is needed.

## Forms and confirmations

Add/edit hospital, renewal and other protected editing tasks use the shared modal. Keep its title/close control visible, allow body scrolling and retain access to footer actions on short screens. Inherit the active appearance.

Use native labeled controls, readonly identity fields, field validation, server errors and a busy state preventing duplicate submission. Cancel, X, Escape and backdrop dismissal protect dirty edits with confirmation inside the same modal. Contain keyboard focus and return it to a visible trigger or page control after closing.

Display abbreviated Thai months, Buddhist Era years and 24-hour `HH:mm` time. Collapse shared date-range components and omit absent time. Native date values stay ISO date-only strings to avoid timezone shifts.

## Operational behavior

- Hospital dates are informational. The operator discusses renewal and explicitly suspends/enables a site; dates never suspend Mobile automatically.
- Usage is distinct authenticated users in 30 days divided by current personnel in the same cohort. Combine sites by summed numerators/denominators, not average percentages.
- Android/iOS installations remain separate from people, sessions, requests and Store downloads. Show period, coverage and freshness. Unsupported/stale data is not zero.
- Growing lists use bounded server pagination and server filters. Fetch only the first page; allow one next-page request, preserve rows/retry on failure, deduplicate IDs and ignore stale filtered responses.
- Management data uses authenticated `/admin/api` with cookie/CSRF protections. Theme changes preserve these controls.
- Certificate screens reflect actual CSR/import capabilities. Automatic signing/renewal is unavailable and must not be presented as successful.
- Go API build/update, Jenkins integration and TAR packages remain paused. ERP deployment/migrations and Provider ID/ThaID activation are outside this release.

## Build and verification

Angular builds to `frontend/dist/browser/` with base URL `/admin/`. Native tooling and Docker embed those static files in Go. Production runs one Go service/image without Node. Deep links return the SPA; missing assets and Admin API paths retain their own errors.

The document supplies a fresh CSP nonce to Angular without `unsafe-inline`. Verify both appearances, narrow layouts, validation, dirty-close behavior, session expiry, pagination/retry and unavailable analytics. Backend tests preserve routing/authentication and manual expiry control.
