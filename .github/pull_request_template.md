## Description

Briefly describe the changes introduced in this pull request and the motivation behind them.

## Type of Change

- [ ] Bug fix (non-breaking change which fixes an issue)
- [ ] New feature (non-breaking change which adds functionality)
- [ ] Breaking change (fix or feature that would cause existing functionality to not work as expected)
- [ ] Documentation update
- [ ] Dependency update / maintenance

## Checklist

- [ ] My code follows the coding style and guidelines of this project.
- [ ] I have run `go test -race ./...` and all tests pass.
- [ ] I have run `go vet ./...` with zero issues.
- [ ] If changing user-facing strings, I have updated both `en.json` and `fa.json` and verified with `go test ./internal/i18n`.
- [ ] If changing database schema, I have appended new migrations to `schema.sql` without altering existing ones.
- [ ] I have updated the documentation accordingly if applicable.
