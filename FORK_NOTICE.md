# Fork Notice

This repository is a fork-based server codebase for **Lastochka Messenger**.

## Upstream Attribution

- Original project: **Tinode Chat Server**
- Upstream repository: https://github.com/tinode/chat
- Upstream license: GPL-3.0 (see `LICENSE`)

The upstream authors and contributors retain copyright to their original work.

## Fork Purpose

This fork is maintained to implement Lastochka-specific backend requirements, integrations, and release workflows while staying aligned with upstream Tinode where feasible.

## License Boundary

- Everything in this repository (upstream code + Lastochka additions) is
  GPL-3.0 (see `LICENSE`). Lastochka-owned additions follow the hygiene rules
  in `org/promts/backend.md` (godoc + unit tests + gofmt/vet/test green).
- Enterprise wrappers (Business Server, Identity, Policy, Compliance,
  Admin UI in `dev/modules/`, `dev/lastochka-business/`) are separate
  programs communicating over the network and are NOT part of this
  repository's GPL scope. Their full license texts are TBD by the owner
  (see `org/docs/job/plans/PLAN_STRATEGY_2026-10-13.md`, Phase 0).
