# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This changelog starts at the initial public release on GitHub; changes prior to that are not tracked here.

## [7.0.0]

### Added
- Initial public release of the NetAcuity Go Client API on GitHub.
- XML UDP query protocol, with feature-code and transaction-ID validation; response-echo verification (transaction ID via string comparison, and IP via `net.ParseIP(...).Equal(...)`) to reject spoofed or stale replies; a `crypto/rand`-backed transaction ID generator; and a `DefaultTimeout` constant used by the `NewClientWithDefaultTimeout` constructor, whose resulting `Client` exposes `QueryXML`.
- Apache License 2.0 (see [LICENSE](LICENSE) and [NOTICE](NOTICE)).
