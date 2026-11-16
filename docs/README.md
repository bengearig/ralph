# ralph design documentation

Specification for the new ralph, written so that an agent with no other context can build from it. The foundational
text is [the Manifesto](../MANIFESTO.md); these docs do not restate it.

## Conventions

- Each doc opens with a status: `draft`, `settled` or `open`. Only settled content is a requirement.
- Requirements are worded so they can be verified.
- Rationale and rejected alternatives live in [decisions](decisions/), not in the specs.
- Unresolved items are tracked in [open-questions.md](open-questions.md).
- These files are the source of truth. While the PoC is in use, they can be mirrored into its store for search.

## Index

| Doc | Covers | Status |
| --- | --- | --- |
| [overview.md](overview.md) | Purpose, principles, goals, non-goals | draft |
| [concepts.md](concepts.md) | Vocabulary, work units | draft |
| [workflow.md](workflow.md) | The method for feature work | draft |
| [architecture.md](architecture.md) | Service layer, local and remote, API server | draft |
| [harness-integration.md](harness-integration.md) | Adapters, interactive surfaces, headless loop | draft |
| [configuration.md](configuration.md) | Layers, setting scope, secrets | draft |
| [documents-and-search.md](documents-and-search.md) | Documents, search, retrieval for the loop | draft |
| [task-manager-and-issues.md](task-manager-and-issues.md) | Task manager mirror and issue source | draft |
| [pull-request-providers.md](pull-request-providers.md) | Pull request providers: interface, choice, credentials, feedback mapping, conformance | draft |
| [local-state.md](local-state.md) | Project root, database, upgrades, backup, run records | draft |
| [first-release.md](first-release.md) | Scope and delivery of the first release | draft |
| [open-questions.md](open-questions.md) | Unresolved design items | draft |
| [decisions/](decisions/) | One record per decision | draft |

Decisions: [0001](decisions/0001-docs-in-repo.md), [0002](decisions/0002-design-principles.md),
[0003](decisions/0003-local-or-hosted-backend.md), [0004](decisions/0004-work-model.md),
[0005](decisions/0005-external-work-items-linking.md), [0006](decisions/0006-shared-state-and-concurrency.md),
[0007](decisions/0007-orphan-task-delivery.md), [0008](decisions/0008-feature-flow.md), [0009](decisions/0009-review-feedback-and-verification.md),
[0010](decisions/0010-sequencing-and-merge-detection.md),
[0011](decisions/0011-after-merge-and-closed-pull-requests.md),
[0012](decisions/0012-service-layer-and-per-service-backends.md), [0013](decisions/0013-implementation-stack.md),
[0014](decisions/0014-server-storage-authentication-tenancy.md), [0015](decisions/0015-pull-request-identity.md), [0016](decisions/0016-harness-integration.md),
[0017](decisions/0017-integration-files-and-headless-permissions.md),
[0018](decisions/0018-configuration-layers-scope-and-secrets.md), [0019](decisions/0019-documents-and-search.md), [0020](decisions/0020-task-manager-and-issue-source.md), [0021](decisions/0021-first-release-scope.md), [0022](decisions/0022-embedding-index-repair.md), [0023](decisions/0023-loop-workspace.md),
[0024](decisions/0024-project-identity-and-local-database.md), [0025](decisions/0025-run-records.md),
[0026](decisions/0026-work-level-and-state-names.md).
