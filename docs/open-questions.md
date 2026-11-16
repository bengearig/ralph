# Open questions

Status: draft

| Question | Blocks |
| --- | --- |
| What is auto mode: what does it start, how is it enabled, and what stops it? | workflow |
| How much of the follow-up to a detected change can be applied automatically, and how is that configured? | workflow |
| Is the monitor's final name `ralph monitor`, and what is the acting command called? | workflow |
| Does branch deletion after merge cover the local branch, the remote branch, or both? | workflow |
| How does ralph read feedback from a pull request, and which feedback becomes a bottom unit? | workflow |
| Does verification run after each bottom unit, after each middle unit, or both? | workflow |
| How is a failed iteration retried, and what is the stall threshold's default? | workflow |
| Where do exported document files go, how are they named, and how does a file say it is generated? | documents |
| How much can the loop push into a prompt, and what is dropped first when the limit is hit? | documents |
| How are chunk size and boundaries chosen, and what Ollama models are supported or required? | documents |
| How are keyword and semantic results combined in one ranking? | documents |
| What is the rebuild command's name, and does it also cover rebuilding the keyword index? | documents |
| Which parts of the embedding configuration make an index stale, and which changes, such as a timeout, do not? | documents |
| How does ralph notice a model updated under the same name when the provider reports no version? | documents |
| How does ralph show progress and an estimate for a rebuild, and what happens when one fails partway? | documents |
| What are the retention and purge rules for earlier document versions (what is configurable, what can be purged, what is protected)? | documents |
| Are earlier versions indexed for search? | documents |
| Should search cover tickets and issues after the first release? | documents |
| Is the fixed set of levels right, or should nesting be arbitrary? | concepts, workflow |
| Does the same in-session option apply to feature work, or only to orphan tasks? | workflow |
| What does ralph do when a ticket whose data it owns is changed in the task manager (overwrite, warn, import)? | task-manager-and-issues |
| Is the bottom unit mapped to tickets by default, and what are the defaults for other levels? | task-manager-and-issues |
| How are ralph's states mapped to Jira's workflow states, including workflows that do not allow every jump? | task-manager-and-issues |
| When a unit starts from a Jira ticket whose type the level mapping does not cover, what does ralph do? | task-manager-and-issues |
| Does ralph comment on or move an issue it started work from, beyond the reference in the pull request? | task-manager-and-issues |
| What does the issue-source interface look like (listing, searching, reading), and how does a user find an issue to start from? | task-manager-and-issues |
| Which system is added after Jira, and in which role? | task-manager-and-issues |
| What are the fingerprint, the stale-write error and the reconciliation path, concretely? | architecture |
| What would permissions attach to (user, team, project, record) so they can be added later? | architecture |
| Which services exist beyond storage and pull request handling (search, logging), and which can be remote? | architecture |
| Which local and remote combinations does the first release support? | architecture |
| How does single-tenant mode map onto organizations and projects? | architecture |
| What do tokens carry (user, scopes, expiry), how are they issued and revoked, and what other methods come later? | architecture |
| How does a service tolerate an unresolvable reference to a record held by another service? | architecture |
| How does a client find and authenticate to a remote instance? | architecture |
| How is the ralph user credited in a pull request description when the shared token is used? | architecture |
| Where is the record of written files kept, how is the managed section marked, and how does `ralph init` remove files? | harness-integration |
| Which other actions, if any, must exist as both CLI commands and MCP tools? | harness-integration |
| What must an adapter declare about a harness's capabilities, and what does ralph do when one is missing? | harness-integration |
| How does the loop build the prompt for a bottom unit, given that linked documents are pushed in (0019)? | harness-integration |
| What does each adapter's restricted allowlist contain, and what is the setting's name, given auto mode is already used in 0011? | harness-integration |
| What are the configuration file names, the command that shows the effective configuration, and how is the project-local file kept out of version control? | configuration |
| Should a remote instance hold settings shared by a team, as a layer between the user file and the project file? | configuration |
| Which secret sources beyond environment variables are supported, and when? | configuration |
| Which of the settings in the decisions so far are user-only? | configuration |
| Should semantic search come before the loop? | first-release |
| OpenCode's headless runs were tested for the loop's needs, with a free model in a scratch repository. The same checks have not been run against Claude Code. Which of them become automated adapter tests? | first-release |
| OpenCode has no cost limit flag, but each step's JSON event reports cost and tokens. Does ralph enforce the budget itself by counting them, for any adapter whose harness reports them, and what does an adapter without either do? | harness-integration |
| In OpenCode a rejected edit is worked around through the shell, so a restricted allowlist must constrain shell commands, not only file edits. How does the adapter interface express an allowlist so each adapter can apply it, and how is it tested against a real run? | harness-integration |
| OpenCode exits with success after denying an action, so denials are found only in its output. How does each adapter report denied actions to the loop for the run record? | harness-integration |
| How many pre-migration backups are kept, and how are old ones removed? | local-state |
| What are the names of the backup command and the setting that requires explicit migration? | local-state |
| How long are run records kept by default, how are they purged, and how are secrets that appear in them handled? | local-state |
| How does the loop behave when the checkout is on a different branch than the unit's, or when the branch already exists? | local-state |
| What is the portable export's format, and when is it designed? | local-state |
| How does a pull request carry a closing reference, for each provider, so that the issue source links or closes the issue? | task-manager-and-issues |
| Which later release adds the API server, and what is its first slice? | first-release |
