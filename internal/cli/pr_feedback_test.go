package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/hosting/fake"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

// feedbackEnv has a finished slice S-1 whose pull request #1 is on a fake host.
func feedbackEnv(t *testing.T) (*quickEnv, *fake.Provider, hosting.PullRequest) {
	t.Helper()
	e := newQuickEnv(t, "")
	e.finishedSlice(t)
	p := fake.New()
	old := providerOpeners["github"]
	providerOpeners["github"] = func(context.Context, *app) (hosting.Provider, error) { return p, nil }
	t.Cleanup(func() { providerOpeners["github"] = old })
	pr, err := p.CreatePullRequest(context.Background(), hosting.NewPullRequest{Head: "ralph/s-1-the-slice", Base: "main", Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := openStore(context.Background(), loadedApp(t), db.Options{MustExist: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	rec := units.PullRequestRecord{SliceID: "S-1", Number: pr.Number, URL: pr.URL, Head: pr.Head, Base: pr.Base, State: units.PullRequestOpen}
	if err := units.NewService(d).RecordPullRequest(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	return e, p, pr
}

func addMixedFeedback(p *fake.Provider) {
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for i, f := range []hosting.Feedback{
		{ID: "c1", Kind: hosting.KindInline, Author: "ann", Body: "Rename this variable to something that says what it holds, please.\nThanks.", Path: "main.go", Line: 7, URL: "https://example.com/c1"},
		{ID: "r2", Kind: hosting.KindReview, Author: "bob", State: hosting.ReviewChangesRequested},
		{ID: "r3", Kind: hosting.KindReview, Author: "cy", State: hosting.ReviewApproved, Body: "LGTM"},
		{ID: "n4", Kind: hosting.KindComment, Author: "di", Body: "Nice work"},
	} {
		f.CreatedAt = at.Add(time.Duration(i) * time.Minute)
		p.AddFeedback(1, f)
	}
}

func TestPRFeedbackTextOutputGolden(t *testing.T) {
	_, p, pr := feedbackEnv(t)
	addMixedFeedback(p)
	head := "S-1 pull request #1 (open): " + pr.URL + "\n"

	code, stdout, stderr := run(t, "pr", "feedback", "s-1")
	want := head + "feedback: 2 new, 0 already handled, 2 not turned into tasks\n" +
		"inline  c1  ann  main.go:7  Rename this variable to something that says what it holds, p…  would create\n" +
		"review  r2  bob  -  -  would create\n" +
		"review  r3  cy  -  LGTM  not a task\n" +
		"comment  n4  di  -  Nice work  not a task\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Fatalf("code = %d, stderr = %q, stdout =\n%q\nwant\n%q", code, stderr, stdout, want)
	}

	code, stdout, stderr = run(t, "pr", "feedback", "S-1", "--create")
	want = head + "feedback: 2 new, 0 already handled, 2 not turned into tasks\n" +
		"inline  c1  ann  main.go:7  Rename this variable to something that says what it holds, p…  T-2\n" +
		"review  r2  bob  -  -  T-3\n" +
		"review  r3  cy  -  LGTM  not a task\n" +
		"comment  n4  di  -  Nice work  not a task\n" +
		"created T-2, T-3\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Fatalf("code = %d, stderr = %q, stdout =\n%q\nwant\n%q", code, stderr, stdout, want)
	}

	code, stdout, _ = run(t, "pr", "feedback", "S-1", "--create")
	want = head + "feedback: 0 new, 2 already handled, 2 not turned into tasks\n" +
		"inline  c1  ann  main.go:7  Rename this variable to something that says what it holds, p…  T-2\n" +
		"review  r2  bob  -  -  T-3\n" +
		"review  r3  cy  -  LGTM  not a task\n" +
		"comment  n4  di  -  Nice work  not a task\n" +
		"no new tasks\n"
	if code != 0 || stdout != want {
		t.Fatalf("code = %d, stdout =\n%q\nwant\n%q", code, stdout, want)
	}
}

func TestPRFeedbackAllFlag(t *testing.T) {
	_, p, _ := feedbackEnv(t)
	addMixedFeedback(p)
	code, stdout, stderr := run(t, "pr", "feedback", "S-1", "--all")
	if code != 0 || !strings.Contains(stdout, "feedback: 3 new, 0 already handled, 1 not turned") || !strings.Contains(stdout, "Nice work  would create") {
		t.Fatalf("code = %d, stderr = %q, stdout = %q", code, stderr, stdout)
	}
}

func TestPRFeedbackRefusedWhenClosed(t *testing.T) {
	_, p, _ := feedbackEnv(t)
	addMixedFeedback(p)
	p.Close(1)
	code, stdout, stderr := run(t, "pr", "feedback", "S-1", "--create")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "is closed") || !strings.Contains(stderr, "tasks are not created") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	code, stdout, _ = run(t, "pr", "feedback", "S-1")
	if code != 0 || !strings.HasPrefix(stdout, "S-1 pull request #1 (closed): ") {
		t.Fatalf("code = %d, stdout = %q", code, stdout)
	}
}

func TestPRFeedbackNoPullRequest(t *testing.T) {
	e := newQuickEnv(t, "")
	e.finishedSlice(t)
	code, stdout, stderr := run(t, "pr", "feedback", "S-1")
	if code != 1 || stdout != "" || stderr != "ralph: S-1 has no pull request: run ralph pr create S-1\n" {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestPRFeedbackJSON(t *testing.T) {
	_, p, pr := feedbackEnv(t)
	addMixedFeedback(p)
	code, stdout, stderr := run(t, "pr", "feedback", "S-1", "--create", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	var got struct {
		Slice       string `json:"slice"`
		PullRequest struct {
			Number int    `json:"number"`
			URL    string `json:"url"`
			State  string `json:"state"`
		} `json:"pull_request"`
		Items []struct {
			ID       string `json:"id"`
			Kind     string `json:"kind"`
			Author   string `json:"author"`
			Path     string `json:"path"`
			Line     int    `json:"line"`
			State    string `json:"state"`
			URL      string `json:"url"`
			Eligible bool   `json:"eligible"`
			Handled  string `json:"handled"`
			Created  string `json:"created"`
		} `json:"items"`
	}
	dec := json.NewDecoder(strings.NewReader(stdout))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	if got.Slice != "S-1" || got.PullRequest.Number != 1 || got.PullRequest.URL != pr.URL || got.PullRequest.State != "open" || len(got.Items) != 4 {
		t.Fatalf("got = %+v", got)
	}
	first, second, third := got.Items[0], got.Items[1], got.Items[2]
	if first.ID != "c1" || first.Kind != "inline" || first.Author != "ann" || first.Path != "main.go" || first.Line != 7 ||
		first.URL != "https://example.com/c1" || !first.Eligible || first.Handled != "" || first.Created != "T-2" {
		t.Errorf("first = %+v", first)
	}
	if second.State != "CHANGES_REQUESTED" || second.Created != "T-3" {
		t.Errorf("second = %+v", second)
	}
	if third.State != "APPROVED" || third.Eligible || third.Created != "" {
		t.Errorf("third = %+v", third)
	}
}

func TestPRFeedbackNeedsOneSliceArgument(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{"pr", "feedback"}, {"pr", "feedback", "S-1", "S-2"}} {
		code, stdout, stderr := run(t, args...)
		if code != 1 || stdout != "" || !strings.Contains(stderr, "accepts 1 arg(s)") {
			t.Errorf("%v: code = %d, stdout = %q, stderr = %q", args, code, stdout, stderr)
		}
	}
}

func TestPRFeedbackNotInitialized(t *testing.T) {
	inRepo(t)
	code, stdout, stderr := run(t, "pr", "feedback", "S-1")
	want := "ralph: this project has no ralph database: run \"ralph init\" first\n"
	if code != 1 || stdout != "" || stderr != want {
		t.Errorf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestPRFeedbackHelpListsFlags(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	code, stdout, stderr := run(t, "pr", "feedback", "--help")
	if code != 0 {
		t.Fatalf("code = %d: %s", code, stderr)
	}
	for _, want := range []string{"--create", "--all", "--json"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, stdout)
		}
	}
}
