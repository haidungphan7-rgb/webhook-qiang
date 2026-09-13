# AGENTS.md

Orientation for coding agents (and humans) working in this repository.

## What this is

A Webhook debugging and replay platform: create inboxes, capture third party requests,
inspect the **raw** payload, and replay an event to a target URL. Go + PostgreSQL +
React, delivered as a single binary with the SPA embedded.

## Non negotiable rules

1. **No code generation in the build path.** The OpenAPI generator was removed on purpose.
   `git clone && go build ./...` must work with only Go installed (Node is needed only to
   rebuild the frontend assets). The API contract is `internal/httpapi` ↔ `web/src/api/v1.ts`
   and is maintained by hand.
2. **Raw bytes are sacred.** Bodies are stored as `bytea` and never trimmed, re-encoded or
   "prettified" on the way in or out. Pretty printing is display only.
3. **Never relax the replay target policy at runtime.** `Policy.dialer` and `Policy.lookupIP`
   are unexported so only `_test.go` can set them. `--replay-allow-host` only narrows;
   `--replay-allow-private` is an explicit, documented demo switch.
4. **Both storage drivers must behave identically.** `internal/storage/mem` exists so tests
   do not need PostgreSQL; if you change a rule in `postgres.go`, mirror it in `mem.go`
   (see `storage.SearchableText` for the pattern).

## Layout

```
cmd/webhook-tester      entrypoint
internal/cli            commands: start, migrate
internal/config         settings + startup validation
internal/http           server, SPA, middlewares (logreq, secure, webhook capture)
internal/httpapi        /api/v1 JSON handlers (hand written DTOs)
internal/capture        sensitive header masking / encryption
internal/replay         target policy (SSRF), execution, retries, recording
internal/storage        domain model + Store interface (+ postgres, mem)
internal/migrate        embedded SQL migrations
internal/retention      background cleanup
internal/crypto         HKDF + AES-GCM, HMAC signing
web/                    React 19 + Mantine 8 SPA
scripts/                demo script, seed data, local test receiver
```

## Commands

```bash
make build        # server binary
make test         # go test -race + vitest
make frontend     # rebuild web/dist
./dev.ps1         # run everything (PowerShell 7)
```

## Testing expectations

- New behaviour in `replay`, `capture`, `crypto`, `storage` or the capture middleware
  needs a unit test. Handler level tests should use the `mem` driver.
- Tests must not weaken the runtime policy: use the existing seams (`dialer`, `lookupIP`).
