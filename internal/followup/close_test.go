package followup_test

import (
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/followup"
	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/prstep"
	"github.com/bengearig/ralph/internal/units"
)

type closeCase struct {
	e      *env
	bare   string
	obs    followup.Observation
	head   string
	branch string
}

// newClose sets up a merged slice whose branch has one commit the base never
// contained, as after a squash merge, pushed to a bare origin.
func newClose(t *testing.T, mode string) *closeCase {
	t.Helper()
	e := newEnv(t)
	bare := t.TempDir()
	e.repo.Git("init", "--bare", "-b", "main", bare)
	e.repo.Git("remote", "add", "origin", bare)
	e.repo.Git("push", "origin", "main")
	s, pr := e.slice(t, "Closing", units.PullRequestMerged)
	branch, err := prstep.BranchName(branchTemplate, s)
	if err != nil {
		t.Fatal(err)
	}
	e.repo.Git("switch", "-c", branch)
	e.repo.WriteFile("work.txt", "work")
	head := e.repo.Commit("work")
	e.repo.Git("push", "origin", branch)
	e.repo.Git("switch", "main")
	e.deps.DeleteBranches = mode
	return &closeCase{e: e, bare: bare, head: head, branch: branch, obs: followup.Observation{
		Slice:  s,
		Record: e.record(t, s),
		Polled: true,
		PullRequest: hosting.PullRequest{
			Number: pr.Number, URL: pr.URL, State: hosting.StateMerged, HeadCommit: head,
		},
		LocalBranch: branch, LocalBranchExists: true,
	}}
}

func (c *closeCase) close(t *testing.T) followup.CloseResult {
	t.Helper()
	res, err := followup.Close(ctx, c.e.deps, c.obs)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (c *closeCase) localExists(t *testing.T) bool {
	t.Helper()
	ok, err := c.e.deps.Workspace.BranchExists(ctx, c.branch)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func (c *closeCase) remoteExists(t *testing.T) bool {
	t.Helper()
	_, ok, err := c.e.deps.Workspace.RemoteBranchTip(ctx, "origin", c.branch)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func (c *closeCase) notes(t *testing.T) []units.Note {
	t.Helper()
	n, err := c.e.units.Notes(ctx, c.obs.Slice.ID)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func hasLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

func (c *closeCase) pushUpstream(t *testing.T) {
	t.Helper()
	other := t.TempDir()
	c.e.repo.Git("clone", c.bare, other)
	c.e.repo.Git("-C", other, "config", "user.name", "Other")
	c.e.repo.Git("-C", other, "config", "user.email", "o@example.com")
	c.e.repo.Git("-C", other, "commit", "--allow-empty", "-m", "upstream")
	c.e.repo.Git("-C", other, "push", "origin", "main")
}

func TestCloseDeletesLocalBranchWhenTipEqualsHead(t *testing.T) {
	c := newClose(t, "local")
	res := c.close(t)
	if c.localExists(t) {
		t.Error("local branch was kept")
	}
	if !hasLine(res.Lines, "deleted local branch "+c.branch) || len(res.Deleted) != 1 || len(res.Refused) != 0 {
		t.Errorf("result = %+v", res)
	}
	if !c.remoteExists(t) {
		t.Error("remote branch was deleted in local mode")
	}
	if res.Slice != c.obs.Slice.ID {
		t.Errorf("slice = %q", res.Slice)
	}
}

func TestCloseKeepsBranchWithExtraCommits(t *testing.T) {
	c := newClose(t, "local")
	c.e.repo.Git("switch", c.branch)
	c.e.repo.WriteFile("more.txt", "more")
	c.e.repo.Commit("more")
	c.e.repo.Git("switch", "main")
	res := c.close(t)
	if !c.localExists(t) {
		t.Error("branch with extra commits was deleted")
	}
	want := c.branch + " has commits that were not in the merged pull request; kept"
	if !hasLine(res.Refused, want) || !hasLine(res.Lines, want) || len(res.Deleted) != 0 {
		t.Errorf("result = %+v", res)
	}
}

func TestCloseRefusesCheckedOutBranch(t *testing.T) {
	c := newClose(t, "local")
	c.e.repo.Git("switch", c.branch)
	res := c.close(t)
	if !c.localExists(t) {
		t.Error("checked-out branch was deleted")
	}
	want := c.branch + " is checked out: git switch main first"
	if !hasLine(res.Refused, want) {
		t.Errorf("result = %+v", res)
	}
}

func TestCloseRefusesWithoutHeadCommit(t *testing.T) {
	c := newClose(t, "both")
	c.obs.PullRequest.HeadCommit = ""
	res := c.close(t)
	want := "cannot tell whether " + c.branch + " holds unmerged work: the host did not report the pull request's head commit"
	if len(res.Refused) != 1 || res.Refused[0] != want || len(res.Lines) != 1 {
		t.Errorf("result = %+v", res)
	}
	if !c.localExists(t) || !c.remoteExists(t) {
		t.Error("a branch was deleted")
	}
	if len(c.notes(t)) != 0 {
		t.Error("a note was added")
	}
}

func TestCloseNoneDoesNothing(t *testing.T) {
	c := newClose(t, "none")
	c.pushUpstream(t)
	res := c.close(t)
	if len(res.Lines) != 0 || len(res.Deleted) != 0 || res.BaseUpdated {
		t.Errorf("result = %+v", res)
	}
	if !c.localExists(t) || !c.remoteExists(t) || len(c.notes(t)) != 0 {
		t.Error("something changed")
	}
}

func TestCloseBothDeletesRemoteWhenTipEquals(t *testing.T) {
	c := newClose(t, "both")
	res := c.close(t)
	if c.localExists(t) || c.remoteExists(t) {
		t.Error("a branch survived")
	}
	if !hasLine(res.Deleted, "deleted local branch "+c.branch) || !hasLine(res.Deleted, "deleted remote branch origin/"+c.branch) {
		t.Errorf("result = %+v", res)
	}
}

func TestCloseBothKeepsRemoteWithExtraCommits(t *testing.T) {
	c := newClose(t, "both")
	c.e.repo.Git("switch", c.branch)
	c.e.repo.WriteFile("more.txt", "more")
	c.e.repo.Commit("more")
	c.e.repo.Git("push", "origin", c.branch)
	c.e.repo.Git("switch", "main")
	c.e.repo.Git("branch", "-f", c.branch, c.head)
	res := c.close(t)
	if c.localExists(t) || !c.remoteExists(t) {
		t.Error("local should be gone and remote kept")
	}
	want := "remote branch " + c.branch + " has commits that were not in the merged pull request; kept"
	if !hasLine(res.Refused, want) {
		t.Errorf("result = %+v", res)
	}
}

func TestCloseBothRemoteAfterLocalRefused(t *testing.T) {
	c := newClose(t, "both")
	c.e.repo.Git("switch", c.branch)
	res := c.close(t)
	if !c.localExists(t) || c.remoteExists(t) {
		t.Error("local should be kept and remote deleted")
	}
	if len(res.Refused) != 1 || !hasLine(res.Deleted, "deleted remote branch origin/"+c.branch) {
		t.Errorf("result = %+v", res)
	}

	c = newClose(t, "both")
	c.e.repo.Git("branch", "-D", c.branch)
	c.obs.LocalBranchExists = false
	res = c.close(t)
	if c.remoteExists(t) || len(res.Deleted) != 1 {
		t.Errorf("remote after absent local: %+v", res)
	}
}

func TestCloseUpdatesBaseWhenOnBaseAndClean(t *testing.T) {
	c := newClose(t, "local")
	c.pushUpstream(t)
	res := c.close(t)
	if !res.BaseUpdated || !hasLine(res.Lines, "updated main from origin") {
		t.Errorf("result = %+v", res)
	}
	if got := c.e.repo.Git("log", "-1", "--format=%s"); got != "upstream" {
		t.Errorf("main head = %q", got)
	}
	res = c.close(t)
	if res.BaseUpdated || !hasLine(res.Lines, "main is up to date") {
		t.Errorf("second result = %+v", res)
	}
}

func TestCloseDoesNotUpdateBaseWhenDirtyOrElsewhere(t *testing.T) {
	c := newClose(t, "local")
	c.pushUpstream(t)
	c.e.repo.WriteFile("dirty.txt", "x")
	res := c.close(t)
	if res.BaseUpdated || !hasLine(res.Lines, "did not update main: the working tree has changes") {
		t.Errorf("dirty: %+v", res)
	}

	c = newClose(t, "local")
	c.pushUpstream(t)
	c.e.repo.Git("switch", "-c", "elsewhere")
	res = c.close(t)
	if res.BaseUpdated || !hasLine(res.Lines, "did not update main: the checkout is on elsewhere") {
		t.Errorf("elsewhere: %+v", res)
	}

	c = newClose(t, "local")
	c.pushUpstream(t)
	c.e.repo.Git("commit", "--allow-empty", "-m", "local only")
	res = c.close(t)
	if res.BaseUpdated {
		t.Errorf("diverged: %+v", res)
	}
	found := false
	for _, l := range res.Lines {
		found = found || strings.HasPrefix(l, "did not update main: it cannot be fast-forwarded from origin")
	}
	if !found {
		t.Errorf("diverged lines = %v", res.Lines)
	}
}

func TestCloseNoteOnlyWhenSomethingDone(t *testing.T) {
	c := newClose(t, "local")
	c.e.repo.Git("switch", c.branch)
	c.close(t)
	if n := c.notes(t); len(n) != 0 {
		t.Errorf("notes after refusal only = %+v", n)
	}
	c.e.repo.Git("switch", "main")
	c.close(t)
	n := c.notes(t)
	if len(n) != 1 || !strings.HasPrefix(n[0].Body, "Merged: "+c.obs.PullRequest.URL+"; ") || !strings.Contains(n[0].Body, "deleted local branch "+c.branch) {
		t.Errorf("notes = %+v", n)
	}
}

func TestCloseTwiceIsQuiet(t *testing.T) {
	c := newClose(t, "both")
	c.close(t)
	before := len(c.notes(t))
	res := c.close(t)
	if len(res.Deleted) != 0 || len(res.Refused) != 0 || res.BaseUpdated {
		t.Errorf("second result = %+v", res)
	}
	if len(c.notes(t)) != before {
		t.Error("second run added a note")
	}
}

func TestPlanCloseWithRemoteBranchOnly(t *testing.T) {
	o := obs(1, units.PullRequestMerged, "")
	o.LocalBranch, o.RemoteBranchExists = "ralph/s-1", true
	if got := followup.Plan([]followup.Observation{o}, followup.PlanOptions{DeleteBranches: "both"}); len(got) != 1 || got[0].Kind != followup.KindClose {
		t.Errorf("both: %+v", got)
	}
	for _, mode := range []string{"local", "", "none"} {
		if got := followup.Plan([]followup.Observation{o}, followup.PlanOptions{DeleteBranches: mode}); len(got) != 0 {
			t.Errorf("%q: %+v", mode, got)
		}
	}
}

func TestObserveRemoteBranchOnlyForMergedInBothMode(t *testing.T) {
	c := newClose(t, "both")
	res := observe(t, c.e, followup.ObserveOptions{})
	if o := c.e.byID(t, res, c.obs.Slice.ID); !o.RemoteBranchExists {
		t.Error("remote branch not seen for merged slice in both mode")
	}
	c.e.deps.DeleteBranches = "local"
	res = observe(t, c.e, followup.ObserveOptions{})
	if o := c.e.byID(t, res, c.obs.Slice.ID); o.RemoteBranchExists {
		t.Error("remote branch looked up in local mode")
	}
}
