# Security

Karaoke is intended for a trusted home network or VPN. Anyone who can reach the server
can search, control playback, change settings and read media from configured libraries.
There are no accounts or access roles. Bind to `127.0.0.1:8765` for use on one computer.

Cross-site write checks, host validation and browser security headers limit requests
from other websites. Network access still determines who can use the application.
Native microphone control is restricted to local TV clients, and an unattended mic
turns off automatically. Browser microphones require localhost or HTTPS.

Use [GitHub's private vulnerability report form](https://github.com/vantuan5644/karaoke/security/advisories/new)
when it is available. If private reporting is unavailable, open an issue asking for a
private contact route, without exploit details or sensitive information. Include the
affected version and reproduction details in the private report. Never attach cookies,
credentials or an unredacted `state.json`.

Security fixes target the current main branch and the latest published release.
