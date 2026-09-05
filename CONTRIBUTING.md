# Contributing to Nodexia

Thank you for your interest in contributing to Nodexia! This document outlines our development workflow, engineering standards, and contribution guidelines.

---

## 🛠️ Development Setup

Nodexia is written in Go (targets **Go 1.26+**) with standard library HTTP routing and server-rendered HTML templates.

### Prerequisites

- Go 1.26+ installed
- Docker (optional, for testing production deployment stacks)
- Make

### Quickstart

1. **Clone the repository:**
   ```bash
   git clone https://github.com/Ho3einK84/Nodexia.git
   cd Nodexia
   ```

2. **Configure environment:**
   ```bash
   cp .env.example .env
   ```
   Set `NODEXIA_AUTH_USERNAME` and `NODEXIA_AUTH_PASSWORD` in `.env`.

3. **Run locally:**
   ```bash
   go run ./cmd/nodexia/
   ```
   Open `http://localhost:8080` in your browser.

4. **Run tests and static analysis:**
   ```bash
   make test              # Runs all unit and integration tests
   go test -race ./...    # Runs tests with Go race detector
   go vet ./...           # Runs Go static analyzer
   make build             # Builds the binary into bin/nodexia
   ```

---

## 📐 Architecture & Coding Guidelines

Before submitting PRs, please review [`docs/architecture.md`](docs/architecture.md) and [`docs/tab-system.md`](docs/tab-system.md).

### Core Principles

- **Minimal Dependencies:** Nodexia maintains a small, auditable dependency footprint. Avoid adding third-party packages when standard library packages suffice.
- **Driver Portability:** Database queries must remain portable between SQLite and MySQL. All shared paths use `db.DBTX`.
- **Database Migrations:** Schema changes in `schema.sql` are **append-only**. Never rewrite or delete existing migration statements.
- **Bilingual Parity:** User-facing strings must be added to both `internal/i18n/locales/en.json` and `internal/i18n/locales/fa.json`. The catalog key parity is strictly enforced by `internal/i18n/parity_test.go`.
- **Secret Isolation:** SSH passwords and private keys are runtime-only; they are never persisted or exported in standard backups.

---

## 🔀 Git & Commit Conventions

We follow the [Conventional Commits](https://www.conventionalcommits.org/) specification:

- `feat(scope): add feature description`
- `fix(scope): fix bug description`
- `docs(scope): update documentation`
- `test(scope): add or improve tests`
- `chore(scope): maintenance or dependency upgrade`

Keep commit summaries in lowercase, imperative mood, and under 72 characters.

---

## 📋 Pull Request Process

1. Fork the repository and create a feature branch from `main`.
2. Ensure existing tests pass and add unit/integration tests for your changes.
3. Verify that `go test -race ./...` and `go vet ./...` pass with zero failures or warnings.
4. If you add or modify translation keys, run `go test ./internal/i18n` to verify key parity.
5. Open a pull request using the standard PR template, describing your changes and verification steps.
