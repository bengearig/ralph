# Documents and search

Status: draft

Rationale is in [0019](decisions/0019-documents-and-search.md). Terms are in [concepts.md](concepts.md).

## Documents

A document holds architecture, design, decisions or reference material. It is a record in ralph's store, and the store
is its source. Documents are project-wide or tied to a top-level unit.

- A document has a kind: architecture, design, decision or reference. Its scope, project-wide or tied to one brief, is
  fixed when it is created. Its ID has the form `D-n`.

- A document is created and edited through ralph, so it has a fingerprint and shared edits are protected as in
  [0006](decisions/0006-shared-state-and-concurrency.md). A write based on a stale read is rejected with a path to
  reconciliation. Only an active document can be edited.
- Documents can be exported to files in the repository, so they can be reviewed through a pull request. Export is
  one-way, from the store to files, in the first release. There is no import, so there is no conflict to resolve. The
  exported files are copies; an edit to one does not change the document.
- `ralph docs export` writes one markdown file for each active document into the directory of the
  `documents.export.dir` setting, `docs/ralph` by default. A file is named `<ID>-<slug>.md`, such as
  `D-12-storage-layout.md`, from the document's ID and a slug of its title. It starts with the line
  `<!-- ralph:generated document=D-n version=N -->`, which is what marks a file as ralph's, and then holds the title as
  a heading, a visible notice that the file is generated and that the next export overwrites it, the kind and the brief
  if the document is tied to one, and the body. The notice and the marker make the document the winner: an edited
  file is overwritten by the next export.
- Export owns only files in that directory whose name has the form `D-n-<slug>.md` and whose first line is a marker. It
  never changes any other file. If a document's file name is taken by a file ralph did not write, that name is reported
  as a conflict, the other documents are still exported, and the command exits 1. A generated file whose document is
  archived, superseded or renamed, or no longer exists, is stale and is removed; each creation, update and removal is
  reported. A file whose content is already right is left alone, so its modification time does not change.
- `ralph docs export --check` writes nothing and exits 1 if an export would change a file or conflict, and `--json`
  prints the result as one JSON object. Nothing is committed, and no actor, time or fingerprint is written to a file, so
  the files change in a commit only when a document does and a pull request shows only the documents that changed.
  The setting must name a directory inside the project and outside `.git` and `.ralph`; otherwise every ralph command
  reports a configuration error.
- A document can be archived or superseded. An archived document is no longer in use. A superseded document has been
  replaced by another and must point to its replacement, which has to be an active document other than itself; there is
  no superseding without a replacement. An archived or superseded document cannot be edited until it is restored, which
  makes it active again and clears the pointer. Archiving, superseding and restoring change the status, not the
  content, so they do not create versions.
- Archived and superseded documents are left out of search results unless the search asks for them.
- An edit that changes the title, body or kind keeps the earlier content as an earlier version; an edit that changes
  nothing leaves the version and the fingerprint as they were. All earlier versions are kept unless the
  `documents.versions.keep` setting limits them: `0`, the default, keeps every earlier version, and a positive number
  keeps that many of the most recent for each document.
- `ralph docs purge` removes earlier versions when a large database needs cleaning up. `--keep N` protects the newest
  `N` earlier versions of each document, `--older-than DURATION` (such as `720h` or `90d`) removes earlier versions made
  longer ago than that, and `--document D-n` limits it to one document; at least one of `--keep` and `--older-than` is
  required. Nothing is removed without `--yes`: without it the command lists what it would remove and exits. A
  document's current content is never removed.

## Search

Search covers documents and work records. It does not cover code, which harnesses already search well, and it does not
cover tickets and issues in the first release.

- Keyword and semantic search are both available from the first release, behind one search interface. Search is a
  service in the sense of [architecture.md](architecture.md), so it can be local or remote.
- Semantic search needs an embedding provider, configured like any other service. Ollama is the provider in the first
  release. It can run on the user's machine, or be hosted alongside the API and MCP server for a shared instance. Other
  providers are later.
- The provider is chosen by `embeddings.provider`, which is `none` by default. The other settings are
  `embeddings.url`, `embeddings.model`, `embeddings.document_prefix` and `embeddings.query_prefix` (the text put
  before a chunk and before a query; empty means the model's built-in prefix and `none` means no prefix),
  `embeddings.chunk_chars` and `embeddings.timeout`.
- All of these settings are user-only. A chunk of a document is sent to the address in `embeddings.url`, so a committed
  file must not be able to choose where a user's text goes. In the committed project file they are ignored with a
  warning, as in [configuration.md](configuration.md); the user file, the project-local file and the environment
  (`RALPH_EMBEDDINGS_PROVIDER`) set them.
- Content is split into chunks for indexing. A record is cut at its headings and blank lines, with a fenced code block
  kept whole, and a block that is too long is cut at sentence ends, into pieces of about `embeddings.chunk_chars`
  characters (1200 by default, from 200 to 6000). Each chunk is embedded
  with its title and the headings above it, so a piece far down a long document still says what it belongs to. A
  record with no text is embedded by its title.
- The vectors are kept in a separate local file in the ralph directory, `.ralph/index.db`, not in the database. It is
  derived from the records, so it is excluded from version control and from the database's backup, and deleting it
  loses nothing but the time to embed again.
- The index follows the records. When a document or work record changes, its index entries are updated, and a stale
  entry never outlives its record: an entry made from text the record no longer has is ignored when a search reads it,
  and `ralph index` removes it.
- `ralph index` embeds every brief, slice, task, document and note that is missing from the index or has changed, in
  that order, and removes entries of records that are gone. It continues where an interrupted run stopped, and says when
  the index is up to date. `ralph index --status` embeds nothing: it shows the provider, the model and its digest,
  whether the provider is reachable, the size of the vectors and how many records are indexed, waiting and to remove
  (`--json` prints the same as one object). It also shows the configuration the index was built with and its state:
  `ok`, `empty` or `stale` with the reasons, and the progress of an unfinished rebuild. `ralph index --rebuild` is
  described under Keeping the semantic index valid.
- Before a search, ralph catches the index up with a few of the records that are waiting, so a record just written is
  found in the same search. What is still waiting is reported in a notice that gives the count and says to run
  `ralph index`; the search still returns results.
- Keyword and semantic results are combined in one ranking by reciprocal rank fusion: each list is ranked best first,
  and a record scores the sum of one over a constant plus its rank in each list. A record found both ways therefore
  ranks above one found one way. The result's `mode` is `hybrid`, and each hit's `sources` says how it was found:
  `keyword`, `semantic` or both. A hybrid score is a rank score and cannot be compared with a keyword score.
- When the provider cannot be used, search does not fail. It returns the keyword results with `mode` set to
  `keyword-fallback` and a notice that gives the reason, such as the provider being unreachable, a model that is not
  installed (with the command to install it), or an index with no vectors yet. After a failure ralph does not ask the
  provider again for a short time, so each search is not slowed by waiting for it.
- A project with no embedding provider configured has keyword search only: `mode` is `keyword`, there are no
  `sources`, and no index file is made.

Keyword search covers a document's current content and the briefs, slices, tasks and notes of the plan, including a
task's acceptance criteria.

- Search does not cover earlier versions of a document. They are history, not the current record, and indexing them would
  fill results with text the document no longer says. They are read with the versions of the document instead.
- The database keeps the index in the same transaction as each change to a record, so the index cannot be stale and
  needs no rebuilding for keyword search.
- A query is plain words, not search syntax: punctuation has no special meaning, and double quotes keep a phrase
  together. A record must contain every word. Only when no record does, the search retries so that a record matching
  some of the words is returned, and the result says it was relaxed.
- Results are ranked best match first. A match in a title ranks above a match in the body, and words match their
  stemmed forms, so `themes` finds `theme`.
- Each result gives its kind, its ID, its title and a snippet with the matching words marked. A note is returned with the
  ID of the unit it is on.
- Archived and superseded documents, cancelled tasks, and the notes on a cancelled task are inactive. They are left out
  unless the search asks for them, and then they are marked inactive.
- `ralph search` runs a search from the command line and the MCP tool `search` runs it for an agent, as in
  [harness-integration.md](harness-integration.md). Both take the words and can limit the kinds searched and the number
  of results, and both can include inactive records.

## Keeping the semantic index valid

Embeddings from different configurations cannot be compared. Changing the model, or anything else that changes what a
stored vector means, makes the existing embeddings wrong for the new configuration. See
[0022](decisions/0022-embedding-index-repair.md).

- The index records the embedding configuration that produced it: the provider, the model without a `:latest` tag, the
  model's digest and the vector size where the provider reports them, the document and query prefixes as they are
  used, and the chunk size. A prefix left empty and the same prefix written out by hand are one configuration.
- Settings that do not change a vector are not part of it: the address of the service and the timeout. Moving the same
  model to another address does not make the index stale.
- Ralph compares the recorded configuration with the current one wherever it uses the index. Settings are compared
  without calling the provider. The model's digest is compared with what the provider reports, asked for at most once
  every ten minutes by a process, and a difference is saved in the index so that commands that do not call the provider
  still show it. An index made before the configuration was recorded is stale. A mismatch is a stale index.
- A provider that cannot be reached is a different case. The index is not stale. Semantic search is unavailable for
  that moment, search falls back to keyword search and says so, and nothing is rebuilt. A finding saved earlier is kept.
- While the index is stale, semantic search is off. Search returns the keyword results with `mode` set to
  `keyword-fallback` and a notice that names what differs, such as `model a -> b`, and the command to run. It does not
  return results from the mismatched vectors, and `ralph index` refuses to add to a stale index.
- `ralph index --rebuild` builds a new index from the current records with the current configuration. Without `--yes` it
  prints the plan and changes nothing: why the index is stale, how many records and about how many chunks, the model
  and where it runs. With `--yes` it builds the new index in `.ralph/index.rebuild.db`, printing a running count, and
  when it is complete it replaces the old index and prints that semantic search now uses the model.
- The old index stays in use until the new one is complete, so search keeps working during a rebuild, and a process that
  is already running, such as the MCP server, follows the new index without a restart. A rebuild that stops, because
  of an error or an interruption (exit status 130), leaves the new file, and running the command again embeds only the
  records that are missing. If the configuration changed since, the earlier work is discarded and the plan says so.
  `--json` prints the plan or the result as one object.
- One `ralph index` run writes at a time, guarded by a lock file. A second run, of a plain index or a rebuild, stops
  with a message that another run is in progress. Search and the other commands do not need the lock.
- Ralph never rebuilds on its own. A rebuild costs time, and with a hosted provider it can cost money, so the user
  confirms first.
- Ralph tells the user the index is stale in the places it already speaks to them: the output of its own commands,
  `ralph status`, which also shows the progress of an unfinished rebuild, and `ralph monitor`; `ralph index --status`
  shows the state and the configuration the index was built with. Inside a harness session, the same notice is part of
  what the MCP server returns, once for each change of the index, and the project instruction entries tell the agent to
  relay it and ask the user whether to rebuild. This does not rely on any harness hook.

## Retrieval for the loop

For each bottom unit the loop does both of the following:
- Ralph includes the documents linked to the unit, or to its middle or top-level unit, in the prompt.
- The agent can search for further documents and work records through the MCP server, as in
  [harness-integration.md](harness-integration.md).

A link is explicit: it joins one document to one brief, slice or task, and exists only because someone made it. A
document tied to a brief is not linked to it by that tie; the tie is the document's scope, and the brief has the document
as a link only after `ralph docs link` or the `link_add` tool is run for it. A unit's documents are the ones linked to
it and those linked to its slice and its brief, which a reader sees as the unit's own and the ones inherited, each
inherited document naming the unit it comes from.

- Only an active document can be linked. A missing document is `not_found`, and an archived or superseded one is
  `invalid`; the message for a superseded one names its replacement, and nothing follows the replacement
  automatically.
- A link to a document of any status can be removed, and removing a link that is not there changes nothing.
- A link survives its document being archived or superseded. It is still shown, with the document's status, and a
  superseded document is shown with its replacement.
- `ralph docs link <document> <unit>`, `ralph docs unlink <document> <unit>` and `ralph docs links <id>` make, remove
  and show links; `links` takes a document, to list its units, or a unit, to list its documents and the inherited
  ones. The tools are `link_add` and `link_remove` with kind `document`, `unit_get` for a unit's documents and
  `doc_get` for a document's `linked_units`. `ralph status <unit>` prints the same documents.
- Links are set by the user and by Receive and Articulate when they work out the plan.

Pushing linked documents into the prompt belongs to the loop. The loop limits how much it includes in a prompt. The
limit and how it is applied are open.

Archived and superseded documents are excluded from what the loop pushes and from search results unless requested.

## Open

See [open-questions.md](open-questions.md).
