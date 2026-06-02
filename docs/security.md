# Security Notes

The app is local-first, but it can be bound to a LAN or reverse-proxy interface when the deployment is configured intentionally.

Current controls:

- Local username/password login.
- PBKDF2-HMAC-SHA256 password hashes with per-user salts.
- HTTP-only same-site session cookies.
- Sessions are time-limited and the in-memory session store prunes expired entries with a hard active-session cap.
- Repeated login failures are rate-limited in memory and return `429` with `Retry-After`.
- Login-failure tracking is pruned and capped to avoid unbounded in-memory growth.
- CSRF token checks for mutating auth, diagnostic, notification, Docker-control, and settings requests. Tokens are compared using constant-time comparison.
- Origin and referer checks for mutating requests. Cross-site POSTs are rejected unless the origin or referer matches the current host, `server.public_url`, or `server.allowed_origins`.
- API responses are marked `Cache-Control: no-store`.
- Request bodies for mutating endpoints are capped at 1 MiB.
- Browser hardening headers include `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy: same-origin`, `Cross-Origin-Resource-Policy: same-origin`, and a restrictive content security policy.
- HTTPS deployments emit `Strict-Transport-Security` when `server.public_url` is configured with an `https://` URL.
- Admin/general-user/custom role separation.
- The detailed admin panel and lightweight compact web app are served on separate ports. The compact router does not register `/api/admin/*` endpoints.
- Per-role app visibility. Hidden apps are removed from app lists, incidents, facts, and role-scoped LLM context.
- Docker start/stop/restart actions are admin-only, CSRF-protected, audited, and resolved from the server-side app snapshot before calling Unraid.
- Docker-provided app icon labels are sanitized with the same URL rules as admin icon overrides; unsupported schemes and embedded credentials are ignored.
- Optional Unraid SSH Docker fallback is disabled by default, uses argument-array process execution rather than shell-built commands, disables password authentication for batch runs, and requires a configured host and user. Use a restricted SSH key where possible.
- No unauthenticated admin endpoints.
- Remote bind safety: non-loopback bind addresses require an explicit bootstrap admin password unless `auth.allow_insecure_remote` is enabled for development only.
- Secrets are read from environment variables, local config, or explicitly configured local API key files only.
- `.env`, local config, state, data, logs, and executables are ignored by git.
- Docker logs are admin-only, bounded to a small response, resolved from the current server-side app snapshot, redacted before JSON response, and audited without storing log text.
- Redaction runs before audit entries, log display, notification text, and LLM context. Audit detail redaction walks nested maps and lists.
- The login form does not ship a default password value in the HTML.

Development bootstrap users:

- `admin` / `change-me-now`
- `viewer` / `change-me-now`

Change these before real LAN or WAN-proxied use by setting `HSD_BOOTSTRAP_ADMIN_USERNAME` and `HSD_BOOTSTRAP_ADMIN_PASSWORD` before the first run, then create named users from Admin -> Settings -> Role Access.

For reverse-proxy deployment:

- Set `HSD_BIND_ADDRESS=0.0.0.0` only behind a trusted firewall or reverse proxy.
- Set `HSD_PUBLIC_URL=https://status.example.com`.
- Set `HSD_COOKIE_SECURE=true` when served over HTTPS.
- Set `HSD_ALLOWED_ORIGINS=https://status.example.com` if the proxy origin differs from the request host.
