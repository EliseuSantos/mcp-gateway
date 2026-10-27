# Security Policy

mcp-gateway sits between AI agents and the tools they call, so security reports are taken seriously.

## Supported versions

| Version | Supported |
|---|---|
| `main` | Yes |
| Latest release (`v0.1.x`) | Yes |
| Older releases | No |

## Reporting a vulnerability

Please **do not** open a public issue, discussion or pull request for a security problem.

Report it privately through GitHub: go to the [Security tab](https://github.com/EliseuSantos/mcp-gateway/security) and click **Report a vulnerability** (private vulnerability reporting). Only the maintainer can see the report.

Please include:

- the affected version or commit;
- the component (for example auth, tool allowlist, audit log, proxy);
- steps to reproduce or a proof of concept;
- the impact you expect (for example auth bypass, policy bypass, secret leaking into the audit log).

## What to expect

- An acknowledgement and follow-up in the private advisory, on a best-effort basis (this is a project maintained by one person).
- If the issue is confirmed, a fix released as a patch version and published as a GitHub Security Advisory.
- Credit in the advisory, if you want it.

## Scope

In scope: the gateway code in this repository (`cmd/`, `internal/`) and its default configuration.

Out of scope: the example server in `examples/echo-server` (it is a test fixture, and its `delete_everything` tool is intentionally a no-op), third-party dependencies (report those upstream), and deployments you do not own.
