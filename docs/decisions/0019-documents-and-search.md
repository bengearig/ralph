# 0019: Documents and search

Status: draft

## Context

Agents with no other context need design material, and a shared instance needs documents all its users can find. The
user's own words on concurrent edits ([0006](0006-shared-state-and-concurrency.md)) used a document as the example.

## Decision

- The store is the source of documents. They are created and edited through ralph and can be exported to repository
  files.
- Keyword and semantic search are both in the first release, behind one interface.
- Search covers documents and work records. It does not cover code, tickets or issues in the first release.
- The loop pushes the documents linked to a unit into its prompt, and the agent can search for more through the MCP
  server. Links are set by the user and by Receive and Articulate, and the amount pushed is limited.
- Export of documents to repository files is one-way, from the store to files, with no import.
- The embedding provider in the first release is Ollama, local or hosted with the server.
- A document can be archived or superseded. A superseded document must point to its replacement. Archived and
  superseded documents are not pushed into prompts and are left out of search results unless requested.
- Earlier versions of a document are kept, with configurable retention and a way to purge old versions.

## Alternatives rejected

- Repository files as the source: reviewed through git and with no second copy, but not shared across an instance.
- Keyword search only: a smaller first release, but weaker retrieval for the loop.
- Searching code: harnesses already do it well.
- Pushing only, or pulling only: pushing alone cannot adapt to what a unit needs, and pulling alone leaves the agent
  without the documents the plan assumes it has.

## Consequences

- Edits to documents bypass git review unless they are exported and reviewed there. Teams that want review need the
  export, so it should not be left out for long.
- An exported file can be edited and then differ from its document. Because export is one-way, the document wins, and
  the next export overwrites the file. The export must say so in the file.
- A superseded document must point to its replacement, or an agent could retrieve an outdated decision as current.
- Kept versions grow the database and the index, so retention and purging are part of the first release's design.
- Semantic search brings an embedding provider, chunking and index maintenance into the first release.
- The linking between documents and units must be defined before the loop can push documents.
- A project without an embedding provider degrades to keyword search and does not fail.
