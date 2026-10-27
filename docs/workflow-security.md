# Workflow supply-chain policy (OPS-43)

All direct external actions, including read-only checks, use immutable full commit
SHAs. Version comments identify the intended release. On 2026-10-27 the pins below
were resolved from the official GitHub repository tag API, peeling annotated tags
to commits; action manifests at those commits were inspected (Node 24 runtimes).
This validates release identity, not an assertion that upstream code is bug-free.

The reusable PR workflow runs the policy and synthetic regression tests before
publication gates can pass. Checkout credential persistence is disabled everywhere.
Workflow-level tokens are read-only; publication write scopes are job-local.
Registry passwords remain on the precise login/description step that needs them.
Renovate must propose full-SHA updates for review with release comments; automerge
is disabled for action updates. Review the upstream release and commit diff before
accepting a new hash. Do not weaken checks when private Actions minutes run out.

Run locally:

```sh
python3 -m venv /tmp/workflow-policy-venv
/tmp/workflow-policy-venv/bin/pip install -r tests/workflows/requirements.txt
/tmp/workflow-policy-venv/bin/python scripts/check_workflows.py
/tmp/workflow-policy-venv/bin/python -m unittest discover -s tests/workflows
```

## Upstream release identities

| Action release | Verified commit |
| --- | --- |
| [actions/checkout@v7.0.1](https://github.com/actions/checkout/tree/v7.0.1) | [3d3c42e5aac5ba805825da76410c181273ba90b1](https://github.com/actions/checkout/commit/3d3c42e5aac5ba805825da76410c181273ba90b1) |
| [actions/setup-go@v7.0.0](https://github.com/actions/setup-go/tree/v7.0.0) | [b7ad1dad31e06c5925ef5d2fc7ad053ef454303e](https://github.com/actions/setup-go/commit/b7ad1dad31e06c5925ef5d2fc7ad053ef454303e) |
| [actions/setup-node@v7.1.0](https://github.com/actions/setup-node/tree/v7.1.0) | [949feb2413d6458794dcd2491c4babbbce0c15c1](https://github.com/actions/setup-node/commit/949feb2413d6458794dcd2491c4babbbce0c15c1) |
| [docker/build-push-action@v7.4.0](https://github.com/docker/build-push-action/tree/v7.4.0) | [c3c9e263c25d99ce0380d002d59b67737d91b0dc](https://github.com/docker/build-push-action/commit/c3c9e263c25d99ce0380d002d59b67737d91b0dc) |
| [docker/login-action@v4.6.0](https://github.com/docker/login-action/tree/v4.6.0) | [dbcb813823bdd20940b903addbd779551569679f](https://github.com/docker/login-action/commit/dbcb813823bdd20940b903addbd779551569679f) |
| [docker/metadata-action@v6.2.0](https://github.com/docker/metadata-action/tree/v6.2.0) | [dc802804100637a589fabce1cb79ff13a1411302](https://github.com/docker/metadata-action/commit/dc802804100637a589fabce1cb79ff13a1411302) |
| [docker/setup-buildx-action@v4.4.1](https://github.com/docker/setup-buildx-action/tree/v4.4.1) | [f87e5991a6d7451dcb8d9637bfbc97413f497069](https://github.com/docker/setup-buildx-action/commit/f87e5991a6d7451dcb8d9637bfbc97413f497069) |
| [docker/setup-qemu-action@v4.4.0](https://github.com/docker/setup-qemu-action/tree/v4.4.0) | [99012661954931238ded8c8b007157a8430204e1](https://github.com/docker/setup-qemu-action/commit/99012661954931238ded8c8b007157a8430204e1) |
| [golangci/golangci-lint-action@v9.3.0](https://github.com/golangci/golangci-lint-action/tree/v9.3.0) | [ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a](https://github.com/golangci/golangci-lint-action/commit/ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a) |
| [goreleaser/goreleaser-action@v7.3.0](https://github.com/goreleaser/goreleaser-action/tree/v7.3.0) | [a4f02ab5355a589de60d917a659ce617ed19b3de](https://github.com/goreleaser/goreleaser-action/commit/a4f02ab5355a589de60d917a659ce617ed19b3de) |
| [peter-evans/dockerhub-description@v5.0.0](https://github.com/peter-evans/dockerhub-description/tree/v5.0.0) | [1b9a80c056b620d92cedb9d9b5a223409c68ddfa](https://github.com/peter-evans/dockerhub-description/commit/1b9a80c056b620d92cedb9d9b5a223409c68ddfa) |

## Local validation

Passed: actionlint for every workflow; policy plus 11 regression tests; `make
fmt tidy lint test build release-check` (including 88 browser tests, 20 script
tests, and Go unit/race suites); and `make container-test` using a disposable
network-isolated container/database. Formatting and tidy left application source
and module files unchanged. No publishing workflow was run.
