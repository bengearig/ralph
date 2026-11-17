# Pull request providers

Status: draft

How ralph opens pull requests and reads their state and feedback on a code host. GitHub and Bitbucket Cloud are
supported; Bitbucket Data Center and Server have a different API and are not. Who the pull request is attributed to is in
[0015](decisions/0015-pull-request-identity.md), and how feedback is used is in
[0009](decisions/0009-review-feedback-and-verification.md). Settings are described in
[configuration.md](configuration.md).

## The provider interface

A provider is one repository on one host. A pull request is identified by its number. A provider can:

- create a pull request from a head branch to a base branch, with a title, a body and a draft flag, and report a
  conflict when one is already open for the head;
- find the pull request for a head branch, preferring an open one and otherwise the newest of any state, or report that
  there is none;
- get a pull request by number, or report that it is not found;
- read the review feedback on a pull request, oldest first, optionally only what is newer than a time.

A pull request has a number, a URL, a title, a body, a head, a base, a draft flag, a state, a head commit and, when
merged, the merge commit. The state is one of `open`, `closed` (closed without merging) and `merged`. The head commit is
the tip of the head branch as the host last knew it for the pull request, which for a merged pull request is the commit
that was merged; ralph compares a branch's tip with it to tell whether the branch holds anything the merge did not, and
it is empty when the host does not report one.

A piece of feedback has an identifier that is unique within the pull request, a kind (`review`, `inline` or `comment`),
an author, a body, a path and line for an inline comment, a review state for a review, a time and a URL. A review state is
`APPROVED`, `CHANGES_REQUESTED` or `COMMENTED`.

Every provider returns the same errors, which callers test for with `errors.Is`:

| Error | Meaning |
| --- | --- |
| not found | The pull request, or the repository as far as the token can see, does not exist. |
| unauthorized | The credential is missing or the host refused it. The error names the setting that holds the credential. |
| conflict | A pull request is already open for the head. |
| invalid | The host rejected the request, with its explanation. |
| rate limited | The host asked ralph to wait; the error carries when, if the host said. |
| unavailable | The host failed or could not be reached. |

Error text never contains a token.

## Choosing the provider

`pr.provider` is `auto`, `github` or `bitbucket`, and defaults to `auto`. Under `auto`:

1. A remote whose host is `github.com` selects GitHub, and one whose host is `bitbucket.org` selects Bitbucket Cloud.
2. Otherwise, if `github.owner` and `github.repo` are both set and the `bitbucket` pair is not, GitHub. If the
   `bitbucket` pair is set and the `github` pair is not, Bitbucket. If both pairs are set, ralph asks to set
   `pr.provider`.
3. Otherwise, if `github.api_url` is not the default, GitHub (GitHub Enterprise).
4. Otherwise ralph cannot tell which provider the remote uses, and says to set `pr.provider`, and for GitHub Enterprise
   `github.api_url`.

A remote that is not a URL, such as a local path, is not an error under `auto`; it only means the first rule does not
decide.

## Settings

| Setting | Provider | Scope | Meaning |
| --- | --- | --- | --- |
| `pr.provider` | both | any layer | Which provider to use; `auto` picks it from the remote. |
| `pr.remote`, `pr.base_branch`, `pr.draft`, `pr.timeout` | both | any layer | The remote the branch is pushed to, the branch pull requests target, whether they open as drafts, and the per-request timeout. |
| `github.owner`, `github.repo` | GitHub | any layer | Override what the remote says. |
| `github.api_url` | GitHub | user-only | The API address; set it for GitHub Enterprise. https, or http on a loopback host. |
| `github.token` | GitHub | user-only | A reference to the token, such as `env:GITHUB_TOKEN`, never the token. |
| `bitbucket.workspace`, `bitbucket.repo` | Bitbucket | any layer | Override what the remote says. |
| `bitbucket.api_url` | Bitbucket | user-only | The API address. https, or http on a loopback host. |
| `bitbucket.token` | Bitbucket | user-only | A reference to the token, such as `env:BITBUCKET_TOKEN`, never the token. |
| `bitbucket.user` | Bitbucket | user-only | The Atlassian account email. When set, the token is sent with Basic authentication; when empty, as a Bearer token. |

The token is read when a request is made, so a changed secret is picked up without a restart.

## Credentials

GitHub takes one token, sent as a Bearer token. Pull requests are attributed to whoever the token belongs to.

Bitbucket Cloud has two kinds of credential, told apart by `bitbucket.user`:

- An Atlassian API token with `bitbucket.user` set to the account email. It is sent with Basic authentication, and pull
  requests are the person's own.
- A repository, project or workspace access token with `bitbucket.user` empty. It is sent as a Bearer token and acts as
  a bot identity, so pull requests are the bot's. It only reaches what it was created for.

Either needs permission to read and write pull requests. A missing or rejected credential is an unauthorized error naming
`bitbucket.token`, and `bitbucket.user` when Basic authentication was used. A forbidden answer is the same error, and says
which permission is missing.

## How the providers map states and feedback

| | GitHub | Bitbucket Cloud |
| --- | --- | --- |
| Open | `open` | `OPEN` |
| Merged | closed with a merge time; the merge commit is read | `MERGED`; the merge commit is read |
| Closed without merging | `closed` with no merge time | `DECLINED` and `SUPERSEDED` |
| Create when one is open | 422 saying one already exists, a conflict | 400 saying one already exists, a conflict |
| Rate limit | 403 with no requests left, or 429; resets at the time given | 429; resets after `Retry-After` |

Finding a pull request is the same idea on both: the open one for the head if there is one, otherwise the newest of any
state. On Bitbucket a pull request whose source branch is in another repository (a fork with a branch of the same name)
is ignored.

Feedback is read from several places on each host and merged into one list, oldest first, with ties broken by
identifier:

| Kind | GitHub | Bitbucket Cloud |
| --- | --- | --- |
| `review` | Each submitted review, with its text. A comment-only review with no text and a pending review are skipped. | Each reviewer's current verdict, `approved` or `changes_requested`, with no text, at the time they last acted. |
| `inline` | Review comments, with the path and the line (the original line when the line no longer exists). | Comments attached to code, with the path and the line (`to`, else `from`). |
| `comment` | Conversation comments. | Comments not attached to code, and replies. |

When ralph turns feedback into tasks, a `review` that requests changes and an `inline` comment with text are made into
tasks, a `comment` and a comment-only `review` with text only when the user asks for them, and an approval never. A
Bitbucket `review` has no text, so a request for changes there becomes a task that says to read the pull request, and
a Bitbucket approval is never a task. See
[0009](decisions/0009-review-feedback-and-verification.md).

Where they differ:

- Bitbucket keeps only each reviewer's current verdict. An approval or a request for changes that a later verdict
  replaced is not seen, and a review has no text. GitHub keeps every review with its text.
- Bitbucket comments that are deleted or pending are skipped; resolved comments and replies are included.
- Review identifiers are `review-<id>` on both, where the id is the review's on GitHub and the reviewer's account on
  Bitbucket, so a Bitbucket reviewer has one review whose time moves forward when they change their verdict.
- A host returns lists in pages. Both providers follow them to the end up to a fixed limit, and Bitbucket only follows a
  next link that stays on the configured API address, so credentials are never sent elsewhere.

What a pull request must say so that an issue is linked or closed is not decided here. It depends on the host and on the
issue source, and is in [open-questions.md](open-questions.md).

## Conformance

A suite in `internal/hosting/hostingtest` runs the same checks against any provider, so the interface means the same
thing for each. It covers creating and getting a pull request, the draft flag, a duplicate create being a conflict,
finding with none, preferring an open pull request, taking the newest when none is open, ignoring other branches, an
unknown number, the merged and declined states with the merge commit, feedback of each kind with its fields, ordering, the
time filter, an empty result that is not nil, unique identifiers, and the unauthorized, rate-limited and unavailable
errors.

The suite takes a subject: the provider and the controls that change what is behind it (merge, decline, add a review,
add an inline comment, add a comment, reject the credential, rate limit, bring the host down). A check whose control is
missing is skipped, and the provider tests fail rather than allow that.

It runs against:

- the in-memory fake provider, so the fake is held to the same behavior;
- a fake GitHub API server in `internal/hosting/github/githubtest`, with a provider pointed at it;
- a fake Bitbucket API server in `internal/hosting/bitbucket/bitbuckettest`, twice, once with Bearer and once with Basic
  authentication.

The fake API servers answer only the requests their provider sends, with that provider's real JSON shapes and status
codes, including pagination, and they check the credential. They are ordinary packages, so other tests can use them.

Both providers are also tested against full-size responses in `testdata`, which carry fields the providers do not read.
The Bitbucket responses were written from the published API description, because no Bitbucket account was available when
the provider was written. A read-only live
test for each host is skipped unless enabled; how to run them is in the [README](../README.md#development). Until the
Bitbucket live test has been run by someone with an account, the Bitbucket provider has not been checked against the real
service.

Where the suite asserts something that is not true of every provider, the suite is what changes, and this document says
why. So far there is nothing of that kind.
