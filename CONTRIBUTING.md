# Contributing

Use Go 1.26 or newer and Node.js 22 or newer. The browser UI uses plain ES modules;
there is no package installation or frontend build step.

```sh
make check
```

This runs the Go tests with the race detector, the browser module tests, formatting
checks and `go vet`. Without Make, run:

```sh
go test -race ./...
node --test "web/test/*.test.mjs"
gofmt -l .
go vet ./...
```

Format changed Go files with `gofmt`. Keep tests deterministic and independent of
external services. The LRCLIB integration test makes real network requests and is
opt-in: `LRCLIB_LIVE=1 go test ./internal/lyrics`.

For a playback or microphone change, also describe what you tried in a real browser,
including OS, browser, playback source and relevant audio hardware. Unit tests cannot
establish device latency, browser permissions or current YouTube behavior.

## Reports and pull requests

Include reproduction steps, expected behavior, actual behavior, version, OS and browser.
Remove cookies, credentials, personal song queues, library paths and network addresses
from logs and screenshots before sharing them. See [SECURITY.md](SECURITY.md) for
security reports.

Keep changes focused. Queue state belongs to `internal/room`, downloads to
`internal/media`, and native audio processing to `internal/voice`. Preserve their locks,
cancellation and shutdown behavior. See [the architecture](docs/architecture.md).

Contributions are provided under [CC BY-NC 4.0](LICENSE), with existing third-party
components retaining their licenses. Include the origin and license of new bundled code
in [THIRD_PARTY.md](THIRD_PARTY.md) and preserve its notices in `LICENSES/`.

## Packaging and repository copies

`make dist VERSION=vX.Y.Z` cross-compiles Linux, macOS and Windows for amd64 and arm64.
Run it on Linux with Make, `tar`, `gzip`, `zip` and `sha256sum` available. Each archive
includes the executable, documentation and license notices. `SHA256SUMS` covers the
archives. Set `DIST_DIR` to keep output on a separate build volume.

The standalone repository is <https://github.com/vantuan5644/karaoke>. Homelab can keep
an identical source copy under `apps/karaoke`; copy source and notices only, excluding
`.git`, binaries, `dist`, local settings and runtime data. The workflows run when
`.github/workflows` is at the standalone repository root. Publishing Karaoke requires
only that standalone repository.

Before publishing changes, scan the standalone history and working files with Gitleaks:

```sh
gitleaks git --redact
gitleaks dir --redact
```

`.gitleaks.toml` retains the default rules and allows one exact public YouTube pagination
value in one parser fixture, including its historical versions. It grants no exception
for credentials or other files.
