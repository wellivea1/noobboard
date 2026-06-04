# Security Notes

The app is local-first, but it can be bound to a LAN or reverse-proxy interface when the deployment is configured intentionally.

Current controls:

- Local username/password login.
- PBKDF2-HMAC-SHA256 password hashes with per-user salts.
- HTTP-only same-site session cookies.
- Sessions are time-limited and the in-memory session store prunes expired entries with a hard active-session cap.
- Repeated login failures are rate-limited in memory and return `429` with `Retry-After`.
- Login-failure tracking is pruned and capped to avoid unbounded in-memory growth.
- CSRF token checks for mutating auth, diagnostic, notification, Docker-control, manual status refresh, and settings requests. Tokens are compared using constant-time comparison.
- Origin and referer checks for mutating requests. Cross-site POSTs are rejected unless the origin or referer matches the current host, `server.public_url`, or `server.allowed_origins`.
- API responses are marked `Cache-Control: no-store`.
- Request bodies for mutating endpoints are capped at 1 MiB.
- Browser hardening headers include `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy: same-origin`, `Cross-Origin-Resource-Policy: same-origin`, and a restrictive content security policy.
- HTTPS deployments emit `Strict-Transport-Security` when `server.public_url` is configured with an `https://` URL.
- Admin/general-user/custom role separation.
- The detailed admin panel and lightweight compact web app are served on separate ports. The compact router does not register `/api/admin/*` endpoints.
- Per-role app visibility. Hidden apps are removed from app lists, incidents, facts, and role-scoped LLM context.
- Docker start/stop/restart actions are admin-only, CSRF-protected, audited, and resolved from the server-side app snapshot before calling Unraid. Stop/restart requests also require an explicit confirmation flag and a `confirm_app_id` matching the resolved app ID, so blind disruptive requests fail before the Docker adapter is called. Docker restart prefers Unraid's native restart mutation; stop/start fallback is used only for schema compatibility and reports when recovery may have left the container stopped. Docker control/log adapters require a `container:` object ID or safe container name; arbitrary Unraid `PrefixedID` values and friendly display names are not used as remote Docker targets.
- Docker-provided app icon labels are sanitized with the same URL rules as admin icon overrides; unsupported schemes and embedded credentials are ignored.
- Optional Unraid SSH Docker fallback is disabled by default, uses argument-array process execution rather than shell-built commands, disables password authentication for batch runs, and requires a configured host and user. It is used for SSH-sourced apps, larger SSH-sourced app lists, and GraphQL transport/schema-unavailable failures; GraphQL permission, validation, and not-found errors fail closed instead of falling through to SSH. Use a restricted SSH key where possible.
- No unauthenticated admin endpoints.
- Remote bind safety: non-loopback bind addresses require an explicit bootstrap admin password unless `auth.allow_insecure_remote` is enabled for development only.
- Secrets are read from environment variables, local config, or explicitly configured local API key files only.
- `.env`, local config, state, data, logs, and executables are ignored by git.
- Docker logs are admin-only, bounded to a small response, resolved from the current server-side app snapshot, redacted before JSON response, and audited without storing log text.
- Status history is stored locally in `history.jsonl` beside the JSON database. It contains status transitions and display names, not raw logs or secrets, but it is still local operational state and must remain out of git.
- Redaction runs before audit entries, log display, notification text, and LLM context. Audit detail redaction walks nested maps and lists.
- LLM agent tools are read-only status fetches (no Docker control, shell, or filesystem access). They are off by default, admin-role only (force-disabled for non-admin policies), restricted to an explicit per-tool allowlist, bounded by a per-policy maximum tool-call count, and fail closed when disabled or when the limit is exceeded. Every tool call is audited (`llm.agent_tool`) and operates only on role-filtered, redacted snapshots.
- LLM repair execution is server-side and restart-only. The model never sends a command; it can only return a schema-allowlisted `recommended_action_id` plus a structured target hint. NoobBoard re-resolves that target against a fresh admin snapshot, maps `ask_admin_to_restart_container` to `docker.ActionRestart`, rejects every other recommendation as non-executing, and calls Docker only from the admin-only approval endpoint. Approval requires CSRF/same-origin checks, `agent_control_enabled=true`, a current session-local arm from `POST /api/admin/agent/arm`, a short-lived server-signed token bound to actor/action/target, a single-use replay nonce, a target app with `agent_repair_allowed=true`, a non-blacklisted app, the per-app 10-minute cooldown, and the global 5/hour repair limit. Optional action auto-review can require a selected reviewer model (`same`, `openai/<model>`, `chatgpt/<model>`, or `anthropic/<model>`) to validate the proposed restart against the current redacted admin snapshot and configured reference docs before execution. Reference files are allowlisted to `docs/*`, `README.md`, and `AGENTS.md`, capped by file/total byte limits, and missing or unsafe paths are skipped so secrets such as local auth files are not sent to the reviewer. Reviewer errors or denials fail closed before Docker is called and are audited. After Docker accepts the restart, NoobBoard refreshes status, writes a repair outcome event to history, and returns recovered/still-down verification to chat without retrying. Proposed, auto-reviewed, approved, denied, not-armed, refused, rate-limited, replay-blocked, failed, executed, and verified decisions are audited. Autonomous repair remains unavailable.
- The OpenAI ChatGPT connector login (browser and headless/device-code) uses OAuth 2.0 with PKCE (S256) and a single-use, time-limited `state`. The auth routes are admin-only and CSRF-protected. The browser-flow callback server binds to loopback (`localhost:1455`) only and is offered only when the admin page is opened on the host; LAN devices use the device-code flow. The OAuth issuer is a fixed endpoint, not a user-supplied URL.
- API keys and ChatGPT tokens are write-only in the settings API: responses report only whether a value is set, never the value. They are never logged and are covered by the redaction blacklist (`*_KEY`, `*_TOKEN`, `AUTHORIZATION`). They are persisted in plaintext in the git-ignored runtime settings store, so treat that store (and its host) as sensitive.
- The login form does not ship a default password value in the HTML.

Development bootstrap users:

- `admin` / `change-me-now`
- `viewer` / `change-me-now`

Change these before real LAN or WAN-proxied use by setting `NOOBBOARD_BOOTSTRAP_ADMIN_USERNAME` and `NOOBBOARD_BOOTSTRAP_ADMIN_PASSWORD` before the first run, then create named users from Admin -> Settings -> Role Access.

For reverse-proxy deployment:

- Set `NOOBBOARD_BIND_ADDRESS=0.0.0.0` only behind a trusted firewall or reverse proxy.
- Set `NOOBBOARD_PUBLIC_URL=https://status.example.com`.
- Set `NOOBBOARD_COOKIE_SECURE=true` when served over HTTPS.
- Set `NOOBBOARD_ALLOWED_ORIGINS=https://status.example.com` if the proxy origin differs from the request host.
