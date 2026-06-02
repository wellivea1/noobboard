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

Allow LAN access on a private network only:

```powershell
New-NetFirewallRule -DisplayName "NoobBoard" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 8787,8788 -Profile Private
```

For LAN use, set `server.bind_address` to the mini PC LAN IP or `0.0.0.0` only when the Windows Firewall rule is limited to the private LAN profile.

For phone use, browse to the compact LAN URL from Safari on iOS or Chrome/Edge on Android. The embedded frontend includes standalone web app metadata, safe-area layout handling, and launcher icons so the page can be saved to the iOS home screen or installed as an Android PWA.
