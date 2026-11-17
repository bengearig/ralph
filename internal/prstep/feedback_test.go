package prstep_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/prstep"
	"github.com/bengearig/ralph/internal/units"
)

var feedbackEpoch = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

// openAt opens pull requests on the fake until one has the number, and
// records it as the slice's pull request.
func (e *env) openAt(t *testing.T, number int) hosting.PullRequest {
	t.Helper()
	var pr hosting.PullRequest
	for i := 1; i <= number; i++ {
		var err error
		pr, err = e.provider.CreatePullRequest(ctx, hosting.NewPullRequest{Head: fmt.Sprintf("h%d", i), Base: "main", Title: "t"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if pr.Number != number {
		t.Fatalf("number = %d, want %d", pr.Number, number)
	}
	rec := units.PullRequestRecord{SliceID: e.slice.ID, Number: pr.Number, URL: pr.URL, Head: pr.Head, Base: pr.Base, State: units.PullRequestOpen}
	if err := e.units.RecordPullRequest(ctx, rec); err != nil {
		t.Fatal(err)
	}
	return pr
}

func (e *env) feedback(t *testing.T, number int, f hosting.Feedback) {
	t.Helper()
	if f.CreatedAt.IsZero() {
		e.fbCount++
		f.CreatedAt = feedbackEpoch.Add(time.Duration(e.fbCount) * time.Minute)
	}
	e.provider.AddFeedback(number, f)
}

func (e *env) readFeedback(t *testing.T, opt prstep.FeedbackOptions) prstep.FeedbackReport {
	t.Helper()
	rep, err := prstep.Feedback(ctx, e.deps, e.slice.ID, opt)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func (e *env) taskCount(t *testing.T) int {
	t.Helper()
	tasks, err := e.units.List(ctx, units.Filter{Level: units.LevelTask, ParentID: &e.slice.ID})
	if err != nil {
		t.Fatal(err)
	}
	return len(tasks)
}

func mixed(e *env, t *testing.T, number int) {
	t.Helper()
	e.feedback(t, number, hosting.Feedback{ID: "a", Kind: hosting.KindInline, Author: "ann", Body: "Rename this.", Path: "main.go", Line: 7, URL: "https://x/a"})
	e.feedback(t, number, hosting.Feedback{ID: "bb", Kind: hosting.KindReview, Author: "bob", Body: "Please fix.", State: hosting.ReviewChangesRequested})
	e.feedback(t, number, hosting.Feedback{ID: "ccc", Kind: hosting.KindReview, Author: "cy", State: hosting.ReviewApproved, Body: "LGTM"})
	e.feedback(t, number, hosting.Feedback{ID: "dddd", Kind: hosting.KindComment, Author: "di", Body: "Thanks!"})
	e.feedback(t, number, hosting.Feedback{ID: "eeeee", Kind: hosting.KindReview, Author: "ed", State: hosting.ReviewCommented, Body: "Some thoughts"})
}

func eligibleIDs(rep prstep.FeedbackReport) string {
	var ids []string
	for _, it := range rep.Items {
		if it.Eligible {
			ids = append(ids, it.Feedback.ID)
		}
	}
	return strings.Join(ids, ",")
}

func TestFeedbackNoPullRequestRecorded(t *testing.T) {
	e := newEnv(t)
	_, err := prstep.Feedback(ctx, e.deps, e.slice.ID, prstep.FeedbackOptions{})
	want := "S-1 has no pull request: run ralph pr create S-1"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestFeedbackRefusesNonSlice(t *testing.T) {
	e := newEnv(t)
	_, err := prstep.Feedback(ctx, e.deps, e.tasks[0].ID, prstep.FeedbackOptions{})
	if err == nil || !strings.Contains(err.Error(), "not a slice") {
		t.Fatalf("err = %v", err)
	}
}

func TestFeedbackEligibleInlineAndChangesRequested(t *testing.T) {
	e := newEnv(t)
	e.openAt(t, 1)
	e.feedback(t, 1, hosting.Feedback{ID: "a", Kind: hosting.KindInline, Author: "ann", Body: "x", Path: "f.go", Line: 1})
	e.feedback(t, 1, hosting.Feedback{ID: "bb", Kind: hosting.KindReview, Author: "bob", State: hosting.ReviewChangesRequested})
	rep := e.readFeedback(t, prstep.FeedbackOptions{})
	if got := eligibleIDs(rep); got != "a,bb" {
		t.Fatalf("eligible = %q", got)
	}
	if rep.Slice.ID != e.slice.ID || rep.Record.Number != 1 || rep.PullRequest.State != hosting.StateOpen {
		t.Errorf("report = %+v", rep)
	}
}

func TestFeedbackIgnoresApprovalsAndPlainCommentsByDefault(t *testing.T) {
	e := newEnv(t)
	e.openAt(t, 1)
	mixed(e, t, 1)
	rep := e.readFeedback(t, prstep.FeedbackOptions{})
	if got := eligibleIDs(rep); got != "a,bb" {
		t.Fatalf("eligible = %q", got)
	}
	if len(rep.Items) != 5 {
		t.Errorf("items = %d", len(rep.Items))
	}
}

func TestFeedbackAllIncludesCommentsAndCommented(t *testing.T) {
	e := newEnv(t)
	e.openAt(t, 1)
	mixed(e, t, 1)
	e.feedback(t, 1, hosting.Feedback{ID: "ffffff", Kind: hosting.KindComment, Author: "fe", Body: "  \n "})
	e.feedback(t, 1, hosting.Feedback{ID: "ggggggg", Kind: hosting.KindReview, Author: "gi", State: hosting.ReviewCommented})
	rep := e.readFeedback(t, prstep.FeedbackOptions{All: true})
	if got := eligibleIDs(rep); got != "a,bb,dddd,eeeee" {
		t.Fatalf("eligible = %q", got)
	}
}

func TestFeedbackBitbucketStyleVerdictWithoutTextIsEligible(t *testing.T) {
	e := newEnv(t)
	pr := e.openAt(t, 1)
	e.feedback(t, 1, hosting.Feedback{ID: "v1", Kind: hosting.KindReview, Author: "val", State: hosting.ReviewChangesRequested})
	rep, err := prstep.Feedback(ctx, e.deps, e.slice.ID, prstep.FeedbackOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	task, err := e.units.Get(ctx, rep.Items[0].Created)
	if err != nil {
		t.Fatal(err)
	}
	want := "val left this feedback on the pull request " + pr.URL + ":\n\n> (no text; read the pull request for the reviewer's comments)\n\n" +
		"Make the change the reviewer asks for. If you decide not to, say why in your final answer."
	if task.Body != want {
		t.Errorf("body =\n%q\nwant\n%q", task.Body, want)
	}
}

func TestFeedbackCreateAppendsReadyTasks(t *testing.T) {
	e := newEnv(t)
	pr := e.openAt(t, 1)
	e.feedback(t, 1, hosting.Feedback{ID: "a", Kind: hosting.KindInline, Author: "ann", Body: "Rename this.\nAnd that.", Path: "main.go", Line: 7, URL: "https://x/a"})
	e.feedback(t, 1, hosting.Feedback{ID: "bb", Kind: hosting.KindReview, Author: "bob", Body: "Please fix.", State: hosting.ReviewChangesRequested, URL: "https://x/bb"})
	e.feedback(t, 1, hosting.Feedback{ID: "ccc", Kind: hosting.KindComment, Author: "", Body: "Hmm"})
	rep := e.readFeedback(t, prstep.FeedbackOptions{Create: true, All: true})

	want := []struct{ title, body, criteria string }{
		{
			"Address ann's comment on main.go:7",
			"ann left this feedback on the pull request " + pr.URL + ":\n\n> Rename this.\n> And that.\n\nLocation: main.go:7\nFeedback: https://x/a\n\n" +
				"Make the change the reviewer asks for. If you decide not to, say why in your final answer.",
			"- The change the reviewer asks for in a is made, or the final answer says why it was not.\n- Every verification command passes.",
		},
		{
			"Address changes requested by bob",
			"bob left this feedback on the pull request " + pr.URL + ":\n\n> Please fix.\n\nFeedback: https://x/bb\n\n" +
				"Make the change the reviewer asks for. If you decide not to, say why in your final answer.",
			"- The change the reviewer asks for in bb is made, or the final answer says why it was not.\n- Every verification command passes.",
		},
		{
			"Respond to a reviewer's comment",
			"A reviewer left this feedback on the pull request " + pr.URL + ":\n\n> Hmm\n\n" +
				"Make the change the reviewer asks for. If you decide not to, say why in your final answer.",
			"- The change the reviewer asks for in ccc is made, or the final answer says why it was not.\n- Every verification command passes.",
		},
	}
	prev := e.tasks[len(e.tasks)-1]
	for i, w := range want {
		task, err := e.units.Get(ctx, rep.Items[i].Created)
		if err != nil {
			t.Fatal(err)
		}
		if task.State != units.StateTodo || task.ParentID != e.slice.ID || task.Position <= prev.Position {
			t.Errorf("task %d = %+v after position %d", i, task, prev.Position)
		}
		if task.Title != w.title || task.Body != w.body || task.AcceptanceCriteria != w.criteria {
			t.Errorf("task %d =\n%q\n%q\n%q\nwant\n%q\n%q\n%q", i, task.Title, task.Body, task.AcceptanceCriteria, w.title, w.body, w.criteria)
		}
		prev = task
	}
}

func TestFeedbackCreateTitleCutAt100Characters(t *testing.T) {
	e := newEnv(t)
	e.openAt(t, 1)
	author := strings.Repeat("é", 120)
	e.feedback(t, 1, hosting.Feedback{ID: "a", Kind: hosting.KindInline, Author: author, Body: "x", Path: "p.go"})
	rep := e.readFeedback(t, prstep.FeedbackOptions{Create: true})
	task, err := e.units.Get(ctx, rep.Items[0].Created)
	if err != nil {
		t.Fatal(err)
	}
	if got := []rune(task.Title); len(got) != 100 || got[99] != '…' || got[98] != 'é' {
		t.Errorf("title = %q (%d characters)", task.Title, len(got))
	}
}

func TestFeedbackCreateLinksAndNotes(t *testing.T) {
	e := newEnv(t)
	pr := e.openAt(t, 12)
	e.feedback(t, 12, hosting.Feedback{ID: "comment-5", Kind: hosting.KindInline, Author: "ann", Body: "x", Path: "p.go", Line: 2, URL: "https://x/c5"})
	e.feedback(t, 12, hosting.Feedback{ID: "review-6", Kind: hosting.KindReview, Author: "bob", State: hosting.ReviewChangesRequested})
	rep := e.readFeedback(t, prstep.FeedbackOptions{Create: true})
	links, err := e.units.Links(ctx, rep.Items[0].Created)
	if err != nil || len(links) != 1 || links[0].Kind != units.LinkExternal || links[0].Target != "feedback:12:comment-5" {
		t.Fatalf("links = %+v, %v", links, err)
	}
	notes, err := e.units.Notes(ctx, rep.Items[0].Created)
	if err != nil {
		t.Fatal(err)
	}
	if !hasNote(notes, "Created from review feedback https://x/c5") {
		t.Errorf("notes = %+v", notes)
	}
	notes, _ = e.units.Notes(ctx, rep.Items[1].Created)
	if !hasNote(notes, "Created from review feedback "+pr.URL) {
		t.Errorf("notes = %+v", notes)
	}
}

func hasNote(notes []units.Note, body string) bool {
	for _, n := range notes {
		if n.Body == body {
			return true
		}
	}
	return false
}

func TestFeedbackCreateTwiceCreatesNothingNew(t *testing.T) {
	e := newEnv(t)
	e.openAt(t, 1)
	mixed(e, t, 1)
	first := e.readFeedback(t, prstep.FeedbackOptions{Create: true})
	count := e.taskCount(t)
	second := e.readFeedback(t, prstep.FeedbackOptions{Create: true})
	if e.taskCount(t) != count {
		t.Fatalf("tasks went from %d to %d", count, e.taskCount(t))
	}
	for i, it := range second.Items {
		if it.Created != "" || it.Handled != first.Items[i].Created {
			t.Errorf("item %d = %+v, first = %+v", i, it, first.Items[i])
		}
	}
}

func TestFeedbackHandledEvenWhenNotEligibleNow(t *testing.T) {
	e := newEnv(t)
	e.openAt(t, 1)
	e.feedback(t, 1, hosting.Feedback{ID: "dddd", Kind: hosting.KindComment, Author: "di", Body: "Thanks!"})
	first := e.readFeedback(t, prstep.FeedbackOptions{Create: true, All: true})
	created := first.Items[0].Created
	if created == "" {
		t.Fatal("no task created")
	}
	second := e.readFeedback(t, prstep.FeedbackOptions{Create: true})
	if it := second.Items[0]; it.Eligible || it.Handled != created || it.Created != "" {
		t.Errorf("item = %+v", it)
	}
}

func TestFeedbackCreateRefusedWhenPullRequestClosed(t *testing.T) {
	e := newEnv(t)
	e.openAt(t, 1)
	mixed(e, t, 1)
	e.provider.Close(1)
	_, err := prstep.Feedback(ctx, e.deps, e.slice.ID, prstep.FeedbackOptions{Create: true})
	if err == nil || !strings.Contains(err.Error(), "is closed") || !strings.Contains(err.Error(), "tasks are not created") {
		t.Fatalf("err = %v", err)
	}
	if e.taskCount(t) != 2 {
		t.Errorf("tasks = %d", e.taskCount(t))
	}
	rep := e.readFeedback(t, prstep.FeedbackOptions{})
	if rep.PullRequest.State != hosting.StateClosed || len(rep.Items) != 5 {
		t.Errorf("report = %+v", rep)
	}
}

func TestFeedbackCreateRefusedWhenPullRequestMerged(t *testing.T) {
	e := newEnv(t)
	e.openAt(t, 1)
	mixed(e, t, 1)
	e.provider.Merge(1)
	_, err := prstep.Feedback(ctx, e.deps, e.slice.ID, prstep.FeedbackOptions{Create: true})
	if err == nil || !strings.Contains(err.Error(), "is merged") || !strings.Contains(err.Error(), "tasks are not created") {
		t.Fatalf("err = %v", err)
	}
	if e.taskCount(t) != 2 {
		t.Errorf("tasks = %d", e.taskCount(t))
	}
}

func TestFeedbackWithoutCreateChangesNothing(t *testing.T) {
	e := newEnv(t)
	e.openAt(t, 1)
	mixed(e, t, 1)
	sliceLinks, _ := e.units.Links(ctx, e.slice.ID)
	sliceNotes, _ := e.units.Notes(ctx, e.slice.ID)
	rep := e.readFeedback(t, prstep.FeedbackOptions{All: true})
	for _, it := range rep.Items {
		if it.Created != "" || it.Handled != "" {
			t.Errorf("item = %+v", it)
		}
	}
	if e.taskCount(t) != 2 {
		t.Errorf("tasks = %d", e.taskCount(t))
	}
	linked, err := e.units.UnitsLinkedTo(ctx, units.LinkExternal, "feedback:1:a")
	if err != nil || len(linked) != 0 {
		t.Errorf("linked = %+v, %v", linked, err)
	}
	links, _ := e.units.Links(ctx, e.slice.ID)
	notes, _ := e.units.Notes(ctx, e.slice.ID)
	if len(links) != len(sliceLinks) || len(notes) != len(sliceNotes) {
		t.Errorf("links %d→%d, notes %d→%d", len(sliceLinks), len(links), len(sliceNotes), len(notes))
	}
}

func TestFeedbackPullRequestGoneFromHost(t *testing.T) {
	e := newEnv(t)
	err := e.units.RecordPullRequest(ctx, units.PullRequestRecord{SliceID: e.slice.ID, Number: 9, URL: "https://x/pull/9", Head: "h", Base: "main", State: units.PullRequestOpen})
	if err != nil {
		t.Fatal(err)
	}
	_, err = prstep.Feedback(ctx, e.deps, e.slice.ID, prstep.FeedbackOptions{})
	if err == nil || !strings.Contains(err.Error(), "gone from the host") || !errors.Is(err, hosting.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestFeedbackUsesLatestRecord(t *testing.T) {
	e := newEnv(t)
	if err := e.units.RecordPullRequest(ctx, units.PullRequestRecord{SliceID: e.slice.ID, Number: 1, URL: "u", Head: "h1", Base: "main", State: units.PullRequestClosed}); err != nil {
		t.Fatal(err)
	}
	e.openAt(t, 2)
	rep := e.readFeedback(t, prstep.FeedbackOptions{})
	if rep.Record.Number != 2 {
		t.Errorf("number = %d", rep.Record.Number)
	}
}

func TestFeedbackNewTasksMakeSliceReadyAgain(t *testing.T) {
	e := newEnv(t)
	e.openAt(t, 1)
	e.feedback(t, 1, hosting.Feedback{ID: "a", Kind: hosting.KindInline, Author: "ann", Body: "x", Path: "p.go", Line: 2})
	st, err := e.units.Status(ctx, e.slice.ID)
	if err != nil || st != units.StatusInReview {
		t.Fatalf("status = %q, %v", st, err)
	}
	e.readFeedback(t, prstep.FeedbackOptions{Create: true})
	st, err = e.units.Status(ctx, e.slice.ID)
	if err != nil || st != units.StatusReady {
		t.Fatalf("status = %q, %v", st, err)
	}
}

type failingNotes struct{ units.Service }

func (failingNotes) AddNote(_ context.Context, id string, kind units.NoteKind, body string) (units.Note, error) {
	return units.Note{}, errors.New("disk full")
}

func TestFeedbackPartialFailureNamesTask(t *testing.T) {
	e := newEnv(t)
	e.openAt(t, 1)
	e.feedback(t, 1, hosting.Feedback{ID: "a", Kind: hosting.KindInline, Author: "ann", Body: "x", Path: "p.go", Line: 2})
	e.feedback(t, 1, hosting.Feedback{ID: "bb", Kind: hosting.KindInline, Author: "bob", Body: "y", Path: "p.go", Line: 3})
	e.deps.Units = failingNotes{e.units}
	rep, err := prstep.Feedback(ctx, e.deps, e.slice.ID, prstep.FeedbackOptions{Create: true})
	if err == nil || !strings.Contains(err.Error(), "T-3") || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v", err)
	}
	if len(rep.Items) != 2 || rep.Items[0].Created != "T-3" || rep.Items[1].Created != "" {
		t.Errorf("items = %+v", rep.Items)
	}
}
