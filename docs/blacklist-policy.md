# Blacklist And Redaction Policy

Blacklist entries apply before data is displayed, stored in audit details, sent through notifications, or included in LLM context.

Supported blacklist categories in config:

- App IDs
- Container names
- Service display names
- Folder paths
- Share names
- File paths
- Filename patterns
- Log regex patterns
- Environment variable names
- URL patterns
- Hostnames
- IP addresses
- Usernames

Secrets are also redacted by built-in patterns for API keys, tokens, passwords, cookies, authorization headers, session IDs, and private keys.

General-user views never include hidden apps, hidden container names, admin summaries, raw logs, or audit details.
