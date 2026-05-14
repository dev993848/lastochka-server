# Contributing to Lastochka Server

Thanks for contributing.

## Project Scope

This repository is a **Tinode fork** used for **Lastochka Messenger** server development.

- Upstream Tinode: https://github.com/tinode/chat
- Lastochka-specific work (auth flows, integrations, infra behavior) is tracked here.

## How to Contribute

1. Open an issue describing the bug or enhancement.
2. Fork this repository and create a feature branch.
3. Keep changes focused and add tests when behavior changes.
4. Submit a pull request with:
   - clear problem statement,
   - implementation notes,
   - migration notes (if config/schema changed).

## Pull Request Checklist

- [ ] Code builds locally.
- [ ] Existing tests pass.
- [ ] New tests added/updated for changed behavior.
- [ ] Docs updated (`README`, config docs, API docs) when needed.
- [ ] No secrets or private keys included.

## Compatibility with Upstream Tinode

When possible:

- Keep patches minimally invasive.
- Preserve upstream conventions and code style.
- Reference upstream behavior in PR descriptions.

If you plan to contribute the same patch upstream, follow upstream contribution rules, including any CLA requirements in the Tinode project.
