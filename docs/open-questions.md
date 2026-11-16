# Open questions

Status: draft

| Question | Blocks |
| --- | --- |
| What are the names for the work levels and for the unplanned and ready states? | concepts, workflow |
| How is order between middle units recorded when it matters? | workflow |
| What is auto mode: what does it start, how is it enabled, and what stops it? | workflow |
| How much of the follow-up to a detected change can be applied automatically, and how is that configured? | workflow |
| Is the monitor's final name `ralph monitor`, and what is the acting command called? | workflow |
| Does branch deletion after merge cover the local branch, the remote branch, or both? | workflow |
| How does ralph read feedback from a pull request, and which feedback becomes a bottom unit? | workflow |
| Which wins when project configuration and acceptance criteria both specify verification? | workflow |
| Where does a unit that cannot be made ready go back to? | workflow |
| Does verification run after each bottom unit, after each middle unit, or both? | workflow |
| How is a failed iteration retried, and what is the stall threshold's default? | workflow |
| How do documents relate to repo files (mirror direction, conflicts) and to tracker content? | architecture |
| How does the loop get the right documents as context? | architecture |
| Is the fixed set of levels right, or should nesting be arbitrary? | concepts, workflow |
| What is the entry point's final name? | workflow |
| How does ralph judge ambiguity and "too big for quick work", and what is the threshold for suggesting Receive? | workflow |
| How are unfinalized tasks surfaced, and what happens when finalization's checks fail? | workflow |
| Does the same in-session option apply to feature work, or only to orphan tasks? | workflow |
| Which tracker items, if any, correspond to the top-level and middle units (for example epics)? | architecture |
| Which source-of-truth arrangements for external trackers ship first? | architecture |
| How are sync direction, conflicts and field ownership handled with a tracker? | architecture |
| Which trackers come first (Jira, GitHub Issues, others)? | architecture |
| What are the fingerprint, the stale-write error and the reconciliation path, concretely? | architecture |
| What would permissions attach to (user, team, project, record) so they can be added later? | architecture |
| Which services exist beyond storage and pull request handling (search, tracker sync, logging), and which can be remote? | architecture |
| Which local and remote combinations does the first release support? | architecture |
| How does single-tenant mode map onto organizations and projects? | architecture |
| What do tokens carry (user, scopes, expiry), how are they issued and revoked, and what other methods come later? | architecture |
| How does a service tolerate an unresolvable reference to a record held by another service? | architecture |
| How does a client find and authenticate to a remote instance? | architecture |
| How is the ralph user credited in a pull request description when the shared token is used? | architecture |
| Where is the record of written files kept, how is the managed section marked, and how does `ralph init` remove files? | harness-integration |
| Which actions, if any, must exist as both CLI commands and MCP tools (quick-work finalization is named as both in 0007)? | harness-integration |
| What can users override in the workflow instructions, and where do overrides live? | harness-integration |
| What must an adapter declare about a harness's capabilities, and what does ralph do when one is missing? | harness-integration |
| How does the loop build the prompt for a bottom unit and choose documents as context? | harness-integration |
| What does each adapter's restricted allowlist contain, and what is the setting's name, given auto mode is already used in 0011? | harness-integration |
| Which harness versions are supported, and how does ralph detect an incompatible one? | harness-integration |
| What are the configuration file names, the command that shows the effective configuration, and how is the project-local file kept out of version control? | configuration |
| Should a remote instance hold settings shared by a team, as a layer between the user file and the project file? | configuration |
| Which secret sources beyond environment variables are supported, and when? | configuration |
| Which of the settings in the decisions so far are user-only? | configuration |
| What is the first release's minimal feature set? | overview |
