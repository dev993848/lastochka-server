# Security Policy (Lastochka Server Fork)

This repository is a Tinode fork used by Lastochka Messenger.
For generic Tinode vulnerabilities, please also consider reporting upstream: https://github.com/tinode/chat/security.

## Reporting a Vulnerability

Please report a vulnerability to `security@tinode.co`.

## Do NOT to report:

 * Firebase initialization tokens. The Firebase tokens are really public: they must be distributed with the client applications and consequently are not private by design.
 * Exposed `/pprof` and/or `/expvar`. We know they are exposed. It's intentional and harmless.
 * Exposed Prometheus metrics `/metrics`. Like above, it's intentional and harmless.
 * DMARC policy is not enabled `p=none`. We know and that's the way we like it for now.
 * Weak cipher suites (TLS 1.0) at `*.tinode.co`. Yes, we know. Does not look serious/important.
