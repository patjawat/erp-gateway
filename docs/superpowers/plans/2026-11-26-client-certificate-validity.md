# Client Certificate Validity Implementation Plan

**Goal:** Let the operator select Gateway client validity and copy the matching offline signing command.

**Architecture:** Bind a bounded numeric control and presets to the existing command generator in Angular and embedded Setup. Reuse the signing script without changing PKI behavior.

**Tech Stack:** Angular signals, embedded JavaScript/HTML, existing OpenSSL wrapper.

**Spec:** `docs/superpowers/specs/2026-11-26-client-certificate-validity-design.md`

## Constraints

- Keep current indigo/gold light/dark design; no new dependencies, production/customer service changes or simulators. A temporary local UI preview is permitted for visual checks.
- No ERP, private-key, CA, runtime config or database writes.
- Default 90; whole days 1–365; presets 90/180/365.
- The user selected client validity; no additional design-variant approval is needed for this control.

## Review focus

- Empty, fractional, nonnumeric and out-of-range input cannot produce/copy a misleading signing command.
- Only validated numeric text enters the shell command; CA init and chain commands remain unchanged.
- Copy fallback works on HTTP LAN and the selected text includes DAYS.
- Label/error relationships and mobile/theme layouts remain usable.

## Tasks

- [x] Update Angular CA guide and embedded Setup guide using existing controls/tokens.
- [x] Add meaningful tests for default, presets, custom values, invalid inputs, copy/fallback and unchanged CA commands.
- [x] Document lifetime selection and where the renewed certificate is installed.
- [x] Run frontend tests, production UI build/embed and relevant Go asset checks; review the rendered UI at desktop/mobile sizes.
- [x] Complete independent review and scoped whitespace checks.

Delivery follows verification through Gateway main commit/push; no server deployment is performed here.

## Verification

- Frontend suite: 112 tests passed in 11 files.
- Production Angular build and Go asset embedding passed.
- Go embedded asset checks: 3 tests passed.
- Independent review closed with no outstanding findings; advisory Enter keys cannot submit the enclosing setup/certificate form.
- Actual Angular component preview verified in the browser at desktop and 390px mobile widths, light/dark themes: preset/custom command updates, invalid state, copy success and no mobile page overflow. No certificate was issued and no live configuration was changed.
- Temporary preview server stopped after verification.
