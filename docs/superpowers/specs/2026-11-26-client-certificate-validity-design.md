# Client certificate validity commands

The user selected the validity of the Gateway client certificate, rather than the issuing CA. They want a control that updates the signing command for copying into Terminal on their Mac.

## Approach

Reuse the existing offline signing script's final `DAYS` argument. Preserve the current UI, CA initialization and manual signing/import process. Automatic issuance or moving CA keys into Gateway would change the trust model and is outside scope.

## Behavior

- Add a clearly labeled client-certificate validity control to the Angular CA guide and the embedded first-run guide, defaulting to 90 days, with 90/180/365-day presets and integer values from 1 through 365.
- Update the displayed signing command immediately from the validated number only. Invalid, missing, fractional or out-of-range input shows an inline error, clears the generated signing command and disables its copy button.
- Explain that validity begins when the Mac signs the CSR. Selecting a value neither extends an existing certificate nor changes CA validity.
- Retain existing copy fallback for HTTP LAN, accessible labels/help/error relationships and the existing theme tokens. Do not redesign the page.
- The client certificate and its private key belong on Gateway; APIs keep the public issuing CA and Gateway SPKI pins. Renewing with the same CA and Gateway key normally requires importing the new client certificate only on Gateway.
- CA expiry checks, CA private-key storage, certificate issuance and API configuration remain unchanged.

## Acceptance

Both guides generate copyable commands with the chosen validated DAYS, reject invalid values, retain copy fallback and explain where to install the new certificate. Existing CSR downloads and LAN transport behavior continue to pass their tests.
