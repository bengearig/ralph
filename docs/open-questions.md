# Open questions

Status: draft

| Question | Blocks |
| --- | --- |
| What is the rebuild command's name, and does it also cover rebuilding the keyword index? | documents |
| Which parts of the embedding configuration make an index stale, and which changes, such as a timeout, do not? | documents |
| How does ralph notice a model updated under the same name when the provider reports no version? | documents |
| How does ralph show progress and an estimate for a rebuild, and what happens when one fails partway? | documents |
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
| Does OpenCode offer a way to layer a user's own permission rules under ralph's, so a user could widen a restricted run without letting a project's rules in? | harness-integration |
| Which ralph tools beyond the read-only ones may a loop agent call, for example adding a note to its unit? | harness-integration |
| What does the loop do with the suggestions in a run record: does the monitor show them, or are they applied only by the user? | harness-integration |
| What are the configuration file names, the command that shows the effective configuration, and how is the project-local file kept out of version control? | configuration |
| Should a remote instance hold settings shared by a team, as a layer between the user file and the project file? | configuration |
| Which secret sources beyond environment variables are supported, and when? | configuration |
| Which of the settings in the decisions so far are user-only? | configuration |
| Should semantic search come before the loop? | first-release |
| How many pre-migration backups are kept, and how are old ones removed? | local-state |
| What are the names of the backup command and the setting that requires explicit migration? | local-state |
| What is the portable export's format, and when is it designed? | local-state |
| How does a pull request carry a closing reference, for each provider, so that the issue source links or closes the issue? | task-manager-and-issues |
| Which later release adds the API server, and what is its first slice? | first-release |
