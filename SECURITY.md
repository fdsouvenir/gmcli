# Security Policy

## Supported Versions

| Version | Supported |
| :--- | :--- |
| `latest` (`main`) | :white_check_mark: |
| `1.3.x` | :white_check_mark: |
| `1.2.x` | :white_check_mark: |
| `< 1.2.0` | :x: |

Security updates and patches are applied to the `main` branch and current releases. Users are encouraged to stay updated with the latest release.

## Reporting a Vulnerability

**Please do NOT open public GitHub issues for security vulnerabilities.** Public issues disclose potential vulnerabilities before a fix is available.

Instead, please report security concerns privately using GitHub's **[Private Vulnerability Reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)**:

1. Navigate to the [Security](https://github.com/fdsouvenir/gmcli/security) tab of this repository.
2. Under "Reporting", click **Report a vulnerability**.
   *(Note for maintainers: Private Vulnerability Reporting must be enabled under repository Settings → Code security.)*
3. Fill out the report form with details of the finding.

When submitting a report, please include:
- A clear description of the vulnerability and its potential impact.
- Steps to reproduce or proof-of-concept code/input.
- Affected versions or commits.
- Any suggested remediations or mitigations.

## Local Session & Credential Security

`gmcli` communicates directly with Google Messages Web and operates on sensitive personal data, including:
- Paired cryptographic session keys (`session.json`)
- Google authentication cookies (`cookies.txt` or browser sessions)
- Mobile phone numbers and participant identifiers
- Local message and attachment SQLite databases (`gmcli.db`)

### Recommended Best Practices:
1. **Never commit credentials**: Ensure `session.json`, cookie exports (`cookies.txt`, `cookies.json`), and database files are never committed to version control or shared publicly.
2. **File permissions**: Restrict permissions on session files so they are readable only by your user account. The default store is `$XDG_STATE_HOME/gmcli`, falling back to `~/.local/state/gmcli`:
   ```bash
   chmod 600 "${XDG_STATE_HOME:-$HOME/.local/state}/gmcli/session.json"
   ```
3. **Session revocation**: If you suspect your session tokens or cookie files have been compromised:
   - Immediately open **Google Messages** on your phone.
   - Go to **Device pairing** / **Linked devices**.
   - Tap **Unpair all devices** to invalidate active Tachyon and Gaia session tokens.
   - On the machine that held the session, run `gmcli auth remove --local-only` to delete `session.json`. When the pairing is still valid, plain `gmcli auth remove` asks the phone to drop just this device first.
4. **Server sign-in socket**: `gmcli auth --devtools-socket PATH` relays the sign-in browser's DevTools endpoint over a Unix socket created with mode 0600. Anything that can connect to it can drive the browser while it is open, so keep the socket under a directory only your user can read (for example `$XDG_RUNTIME_DIR`), and reach it from another machine only through an SSH tunnel. gmcli removes the socket when it exits.
5. **Alternate stores**: `GMCLI_STORE` and `--store` point gmcli at another data directory. Each store holds its own `session.json` with the same sensitivity; apply the permission guidance above to every store you create.
