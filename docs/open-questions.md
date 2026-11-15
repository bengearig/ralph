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
| Which harnesses are supported first, and how is a harness integrated? | architecture |
| Where does configuration live and how do layers combine? | configuration |
| What is the first release's minimal feature set? | overview |
