# Contributing to Nodexia

Thank you for contributing to Nodexia! This guide outlines our architecture principles, engineering standards, and contribution process.

---

## 🛠️ Quickstart

Nodexia requires **Go 1.26+** and utilizes standard Go tooling:

```bash
# Clone repository
git clone https://github.com/Ho3einK84/Nodexia.git
cd Nodexia

# Configure environment
cp .env.example .env

# Run local development server
go run ./cmd/nodexia/
```

Access the panel at `http://localhost:8080`.

---

## 📐 Architecture & Engineering Standards

Before submitting PRs, review our core architectural guides:
- [**System Architecture (`docs/architecture.md`)**](docs/architecture.md)
- [**Frontend Architecture (`FRONTEND.md`)**](FRONTEND.md)
- [**Multi-Tab Workspace (`docs/tab-system.md`)**](docs/tab-system.md)

### Key Rules

1. **Zero-Build Frontend:** No `npm`, `vite`, `webpack`, or CSS preprocessors. Use browser-native ES Modules (`web/static/modules/`) and CSS design tokens (`web/static/tokens.css`).
2. **Minimal Dependencies:** Use Go standard library where possible; justify any new external module in `go.mod`.
3. **Database Portability:** Write portable SQL supported by both SQLite and MySQL. All database operations must use the shared `db.DBTX` interface.
4. **Append-Only Migrations:** Never modify existing statements in `schema.sql`; append new migrations at the end.
5. **Bilingual Parity:** Add user-facing strings to both `internal/i18n/locales/en.json` and `fa.json`. Key parity is enforced by automated tests.
6. **Secret Isolation:** SSH credentials and private keys are runtime-only and must never be stored in persistent storage or backups.

---

## 🧪 Verification & Testing

Always ensure tests and analyzers pass before opening a pull request:

```bash
make test             # Run unit, integration, and asset tests
go test -race ./...   # Verify concurrency safety
go vet ./...          # Run static analysis
make build            # Verify binary compilation
```

---

## 🔀 Commit & PR Guidelines

We follow [Conventional Commits](https://www.conventionalcommits.org/):
- `feat(scope): add feature`
- `fix(scope): fix bug`
- `docs(scope): update documentation`
- `test(scope): add tests`
- `chore(scope): maintenance or dependencies`

Keep commit summaries lowercase, imperative, and under 72 characters.
