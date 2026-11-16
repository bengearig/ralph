# Documents and search

Status: draft

Rationale is in [0019](decisions/0019-documents-and-search.md). Terms are in [concepts.md](concepts.md).

## Documents

A document holds architecture, design, decisions or reference material. It is a record in ralph's store, and the store
is its source. Documents are project-wide or tied to a top-level unit.

- A document is created and edited through ralph, so it has a fingerprint and shared edits are protected as in
  [0006](decisions/0006-shared-state-and-concurrency.md). A write based on a stale read is rejected with a path to
  reconciliation.
- Documents can be exported to files in the repository, so they can be reviewed through a pull request. Export is
  one-way, from the store to files, in the first release. There is no import, so there is no conflict to resolve. The
  exported files are copies; an edit to one does not change the document.
- A document can be archived or superseded. An archived document is no longer in use. A superseded document has been
  replaced by another and must point to its replacement.
- Archived and superseded documents are left out of search results unless the search asks for them.
- Earlier versions of a document are kept. How many are kept is configurable, and old versions can be purged when a
  large database needs cleaning up. The retention and purge rules are open.

## Search

Search covers documents and work records. It does not cover code, which harnesses already search well, and it does not
cover tickets and issues in the first release.

- Keyword and semantic search are both available from the first release, behind one search interface. Search is a
  service in the sense of [architecture.md](architecture.md), so it can be local or remote.
- Semantic search needs an embedding provider, configured like any other service. Ollama is the provider in the first
  release. It can run on the user's machine, or be hosted alongside the API and MCP server for a shared instance. Other
  providers are later.
- Content is split into chunks for indexing. Chunk size and boundaries are open.
- The index follows the records. When a document or work record changes, its index entries are updated, and a stale
  entry never outlives its record.
- How keyword and semantic results are combined in one ranking is open.
- A project with no embedding provider configured still has keyword search.

## Keeping the semantic index valid

Embeddings from different configurations cannot be compared. Changing the model, or anything else that changes what a
stored vector means, makes the existing embeddings wrong for the new configuration. See
[0022](decisions/0022-embedding-index-repair.md).

- The index records the embedding configuration that produced it: the provider, the model including its version where
  the provider reports one, the vector size, the prefix settings, and the chunking settings.
- Ralph compares the recorded configuration with the current one wherever it uses the index, and at startup. A mismatch
  is a stale index.
- While the index is stale, semantic search is off. Search falls back to keyword search and says so. It does not return
  results from the mismatched vectors.
- A command rebuilds the index from the current records using the current configuration. It can resume after an
  interruption. The old index stays in use until the new one is complete, so search keeps working during a rebuild. The
  command's name is open.
- Ralph never rebuilds on its own. A rebuild costs time, and with a hosted provider it can cost money, so the user
  confirms first.
- Ralph tells the user the index is stale in the places it already speaks to them: the output of its own commands, the
  project status, and `ralph monitor`. Inside a harness session, the same notice is part of what the MCP server returns,
  and the project instruction entries tell the agent to relay it and ask the user whether to rebuild. This does not
  rely on any harness hook.
- A provider that cannot be reached is a different case. The index is not stale. Semantic search is unavailable for
  that moment, search falls back to keyword search and says so, and nothing is rebuilt.

## Retrieval for the loop

For each bottom unit the loop does both of the following:
- Ralph includes the documents linked to the unit, or to its middle or top-level unit, in the prompt.
- The agent can search for further documents and work records through the MCP server, as in
  [harness-integration.md](harness-integration.md).

Links between documents and units are set in two ways: explicitly by the user, and by Receive and Articulate when they
work out the plan. The loop limits how much it includes in a prompt. The limit and how it is applied are open.

Archived and superseded documents are excluded from what the loop pushes and from search results unless requested.

## Open

See [open-questions.md](open-questions.md).
