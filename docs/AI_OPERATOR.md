# DVHub AI Operator

The AI Operator is an authenticated Admin-only assistant backed by Google
Gemini. It receives a redacted live snapshot of the gateway, routes, service
states, DMR sessions, TG23530 conference, AllStar/EchoLink state, media quality
and pending timed actions.

## Safety model

Gemini never receives network passwords, API keys, Caddy credentials or other
protected configuration. It also has no shell access. Its response is parsed as
a structured proposal and independently checked by the gateway.

The gateway accepts only these proposed actions:

- start, stop or restart the allowlisted YSF, NXDN, P25, M17 and D-STAR
  reflector/bridge services;
- start or stop the permanent TG23530 conference;
- connect or disconnect AllStar node 530471 from a valid remote node;
- disconnect an unprotected manual DMR session.

Protected DMR sessions 1, 2 and 4 cannot be edited individually. Self-links,
unknown services, conflicting plans, arbitrary commands and schedules longer
than seven days are rejected. Every mutation requires an authenticated operator
to review and confirm a one-use plan within ten minutes.

Timed actions are saved in `/var/lib/dvgateway/ai-schedules.json` and restored
after a gateway restart. A timed start or link can automatically reverse at the
requested duration. Existing audio fingerprint suppression and first-talker
control remain authoritative for live loop prevention.

## Gemini configuration

Create `/etc/dvhub/gemini.env` outside Git:

```text
GEMINI_API_KEY=your-google-ai-studio-key
GEMINI_MODEL=gemini-3.8-flash
```

Set ownership to `root:dvhub`, mode `0600`, then restart
`dvhub-gateway.service`. The Admin AI Operator tab reports `READY` only when a
key is available to the service.

Administrators can instead add or replace the key from **Admin → AI Operator**.
The masked field validates it directly with Google before writing it to
`/var/lib/dvgateway/gemini-api-key` with mode `0600`. The stored value is never
returned to the browser. The allowlisted model dropdown writes the selection to
`/var/lib/dvgateway/gemini-model`; changing models does not require re-entering
an already stored key.

The optional `GEMINI_MODEL` environment setting remains available for
externally managed installations. Dashboard-managed settings take precedence.
The recommended default is Gemini 3.8 Flash, followed by higher-cost reasoning
models and then free-tier/low-cost choices. Requests are sent directly from the
VPS to Google's Gemini API with the key in the `x-goog-api-key` header.

## Operating flow

1. Ask a question or describe the desired connection and timing.
2. Gemini explains its diagnosis and creates an allowlisted proposal.
3. Review every action and warning.
4. Confirm once to execute the one-use plan, or reject it with no change.
5. Cancel future actions from the schedule list when needed.

Manual DMR connects still use the DMR tab because network credentials must
never be placed in an AI prompt.
