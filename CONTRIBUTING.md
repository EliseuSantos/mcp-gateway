# Contributing to mcp-gateway

Thanks for taking the time to contribute. This guide covers the setup, the checks CI runs, and how changes get in.

## Setup

You need:

- Go 1.25 or newer (the module line is `go 1.25.0`).
- Docker, to run the example upstream server and Jaeger.
- [golangci-lint](https://golangci-lint.run/) v2.11.4, for `make lint`:

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4
```

Clone and run the stack locally:

```bash
git clone https://github.com/EliseuSantos/mcp-gateway.git
cd mcp-gateway
docker compose up --build
```

Or without Docker:

```bash
make run-example   # echo-server on :8081
make run           # gateway on :8080
```

The [README](README.md) has the full quickstart and the configuration reference.

## Checks

Run the same three targets CI runs on every push and pull request:

```bash
make lint    # golangci-lint v2, including a format check
make test    # go test -race ./...
make build   # writes bin/mcp-gateway and bin/echo-server
```

Please add or update tests for any behavior change, especially in `internal/auth`, `internal/policy` and `internal/audit`.

## Workflow

1. Open an issue first for anything beyond a small fix, so we can agree on the approach. Use the bug or feature template.
2. Fork the repository and create a branch from `main`.
3. Keep the pull request focused on one change and fill in the pull request template.
4. Make sure `make lint`, `make test` and `make build` pass.
5. Design decisions that change behavior or the policy model go in an ADR under [`docs/adr/`](docs/adr) (see `TEMPLATE.md`).

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/) in English, for example:

```text
feat: add per-tool rate limit
fix(policy): deny tools missing from the allowlist
docs: explain JWT audience check
```

## Security issues

Do not open a public issue for a vulnerability. See [SECURITY.md](SECURITY.md).

## License

By contributing, you agree that your contributions are licensed under the [Apache-2.0 License](LICENSE).
