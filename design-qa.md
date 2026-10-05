# Live routing connector design QA

- Source visual truth: `C:\Users\2e0lx\AppData\Local\Temp\codex-clipboard-714ea934-73af-4a94-9eda-b33117674c78.png`
- Source pixels: 1024 × 512; desktop crop of the routing panel at 1× density.
- Implementation: `http://127.0.0.1:4173/dashboard.html`
- Implementation screenshot: Codex in-app browser tab 8 captures recorded inline in the task; the browser runtime did not expose a filesystem path.
- Implementation viewport: 1280 × 720 CSS px at 1× density.
- States checked: NXDN active input and idle/no-transmission.

## Full-view comparison evidence

The implementation preserves the reference composition: centred source card, one-to-many curved connectors, and a three-column route-card grid. The requested change is intentionally visible: connectors render above the route-card layer with transparent base paths. In the active state, NXDN is identified with amber styling and an inward-moving amber path; the eight healthy destinations use outward-moving green paths. The idle state contains nine faint connector paths and no animated overlays.

## Focused region comparison evidence

The routing panel was inspected at readable scale because connector/card intersections and route labels are the fidelity-critical region. Lines terminate at card edges, do not cover route text, and the SVG overlay has `pointer-events: none`. The source card remains above the connector layer. Computed animation offsets moved in opposite directions during a 240 ms observation: outgoing paths became more negative while the NXDN incoming path became more positive.

## Required fidelity surfaces

- Fonts and typography: existing dashboard families, weights, sizes and hierarchy are preserved.
- Spacing and layout: source-card and three-column card geometry match the supplied reference; no visible overlap or clipping at 1280 × 720.
- Colours and tokens: existing green/amber semantic tokens are reused; idle path opacity is 0.22.
- Image quality and assets: no raster assets are required for this data-driven connector visualization; existing vector paths remain crisp at browser scale.
- Copy and content: the legend now explains inbound amber, outbound green and idle transparent routes.

## Findings

No actionable P0, P1 or P2 visual mismatches remain. The wider implementation viewport includes the dashboard health rail outside the narrower source crop; this is existing responsive product layout rather than design drift in the routing panel.

## Comparison history

- Initial implementation comparison: passed with no P0/P1/P2 finding, so no visual repair iteration was required.
- Browser console: no relevant warnings or errors in active or idle mock states.
- Interaction proof: NXDN resolved as the single origin; eight other healthy routes resolved as receivers; animation offsets advanced in opposite directions.

## Follow-up polish

- P3: verify an unusually narrow mobile browser during production smoke testing; the existing two-column mobile card breakpoint was preserved.

final result: passed
