# Windows Deployment

Build:

```powershell
& 'C:\Program Files\Go\bin\go.exe' build -o dist\noobboard.exe .\cmd\dashboard
```

Foreground test run:

```powershell
.\dist\noobboard.exe serve
```

Default local URLs:

```text
Admin panel: http://127.0.0.1:8787
Compact web app: http://127.0.0.1:8788
```

Default Windows paths:

- Config: `C:\ProgramData\NoobBoard\config.yaml`
- Database: `C:\ProgramData\NoobBoard\data\dashboard.db.json`
- Logs: `C:\ProgramData\NoobBoard\logs\`

Install service from an elevated PowerShell prompt:

```powershell
.\dist\noobboard.exe install-service
.\dist\noobboard.exe start-service
```

Or use the one-step installer, which also prompts for the firewall rule and the admin login:

```powershell
.\install.ps1 -Start
```

Admin login via the installer (and the install/wizard contract):

- When you accept the admin-login prompt, `install.ps1` writes `auth.bootstrap_admin_username`
  and `auth.bootstrap_admin_password` into `C:\ProgramData\NoobBoard\config.yaml` and
  restricts that file to Administrators/SYSTEM (the service runs as LocalSystem).
- These are **bootstrap** credentials: the admin user is created from them on the service's
  **first run** and the password is then hashed into the database. Bootstrap is create-only,
  so changing these values later does **not** rotate an existing admin password — use the
  app's settings (or the setup wizard) to change an existing login.
- The password is **not removed** from `config.yaml` after first run; it remains there in
  plain text (file restricted to Administrators/SYSTEM). Treat the file as a secret, and
  delete the `bootstrap_admin_password` line after first sign-in if you want it gone.
- The installer never sets a "setup complete" marker. When the in-app setup wizard ships, it
  still runs and simply continues from the accounts step (an admin already exists). See the
  "Installer interop contract" in `docs/agent-roadmap.md`. Anything that later writes
  `config.yaml` must merge/preserve existing `auth:` keys rather than overwrite the file.

Firewall: the installer prompt (and `-AllowLan`) opens TCP 8787-8788 on the **Private and
Public** profiles, because Windows often mislabels a home LAN as "Public". This does not
isolate the host if it is genuinely internet-facing — change the default admin password and
prefer an HTTPS reverse proxy for real WAN access. To restrict to the private profile only,
add the rule manually instead:

```powershell
New-NetFirewallRule -DisplayName "NoobBoard" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 8787,8788 -Profile Private
```

For LAN use, set `server.bind_address` to the mini PC LAN IP or `0.0.0.0` only behind a trusted firewall/router.

For phone use, browse to the compact LAN URL from Safari on iOS or Chrome/Edge on Android. The embedded frontend includes standalone web app metadata, safe-area layout handling, and launcher icons so the page can be saved to the iOS home screen or installed as an Android PWA.
