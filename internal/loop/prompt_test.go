package loop_test

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/doclinks"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/loop"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

var (
	ctx    = context.Background()
	update = flag.Bool("update", false, "rewrite golden files")
)

type fixture struct {
	units *units.Store
	docs  *documents.Store
	links *doclinks.Service
	brief units.Unit
	slice units.Unit
	task  units.Unit
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d := testutil.NewDB(t, db.Migrations)
	u, ds := units.New(d), documents.New(d)
	clock := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	u.Now = func() time.Time { clock = clock.Add(time.Minute); return clock }
	f := &fixture{units: u, docs: ds, links: doclinks.New(u, ds)}
	f.brief = f.unit(t, units.LevelBrief, "", "Ship it", "The brief body.", "")
	f.slice = f.unit(t, units.LevelSlice, f.brief.ID, "The slice", "The slice body.", "")
	f.task = f.unit(t, units.LevelTask, f.slice.ID, "Do the thing", "Task body.", "- it works\n- it is tested")
	return f
}

func (f *fixture) unit(t *testing.T, level units.Level, parent, title, body, criteria string) units.Unit {
	t.Helper()
	n, err := f.units.Create(ctx, units.NewUnit{Level: level, ParentID: parent, Title: title, Body: body, AcceptanceCriteria: criteria})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *fixture) doc(t *testing.T, title, body, linkTo string) documents.Document {
	t.Helper()
	d, err := f.docs.Create(ctx, documents.NewDocument{Kind: documents.KindDesign, Title: title, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if linkTo != "" {
		if err := f.links.Link(ctx, d.ID, linkTo); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func (f *fixture) note(t *testing.T, body string) {
	t.Helper()
	if _, err := f.units.AddNote(ctx, f.task.ID, units.NoteGeneral, body); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) deps() loop.PromptDeps {
	return loop.PromptDeps{Units: f.units, Links: f.links, Docs: f.docs}
}

func (f *fixture) build(t *testing.T, in loop.PromptInput) loop.Prompt {
	t.Helper()
	if in.TaskID == "" {
		in.TaskID = f.task.ID
	}
	if in.Attempt == 0 {
		in.Attempt, in.MaxAttempts = 1, 3
	}
	p, err := loop.BuildPrompt(ctx, f.deps(), in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("prompt differs from %s:\n%s", path, got)
	}
}

func (f *fixture) fullScenario(t *testing.T) {
	t.Helper()
	other := f.unit(t, units.LevelTask, f.slice.ID, "Another task", "x", "")
	_ = other
	f.note(t, "first note")
	f.note(t, "second note")
	f.doc(t, "Slice doc", "Slice doc text.\n", f.slice.ID)
	f.doc(t, "Task doc", "Task doc text.", f.task.ID)
}

var verifyCmds = []string{"go test ./...", "make lint"}

func TestPromptGoldenFirstAttempt(t *testing.T) {
	f := newFixture(t)
	f.fullScenario(t)
	p := f.build(t, loop.PromptInput{Verify: verifyCmds})
	golden(t, "first_attempt.golden", p.Text)
}

func TestPromptGoldenRetry(t *testing.T) {
	f := newFixture(t)
	f.fullScenario(t)
	p := f.build(t, loop.PromptInput{
		Verify: verifyCmds, Attempt: 2, MaxAttempts: 3,
		Previous: &loop.Failure{
			Line: "Verification failed: go test ./... exited with 1.",
			Denials: []harness.Denial{
				{Tool: "Bash", Target: `{"command":"rm -rf /"}`, Reason: "not allowed"},
				{Tool: "WebFetch", Reason: "network is off"},
			},
			VerificationOutput: "--- FAIL: TestX\n```\nboom\n```\n",
		},
	})
	golden(t, "retry.golden", p.Text)
}

func TestPromptOrphanTaskIsError(t *testing.T) {
	f := newFixture(t)
	orphan := f.unit(t, units.LevelTask, "", "Loose", "b", "")
	_, err := loop.BuildPrompt(ctx, f.deps(), loop.PromptInput{TaskID: orphan.ID, Attempt: 1, MaxAttempts: 1})
	want := orphan.ID + " has no slice; the loop works the tasks of a slice"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestPromptNotATaskIsError(t *testing.T) {
	f := newFixture(t)
	_, err := loop.BuildPrompt(ctx, f.deps(), loop.PromptInput{TaskID: f.slice.ID})
	if err == nil || !strings.Contains(err.Error(), f.slice.ID) {
		t.Fatalf("err = %v", err)
	}
}

type briefless struct {
	units.Service
	slice string
}

func (b briefless) Get(ctx context.Context, id string) (units.Unit, error) {
	u, err := b.Service.Get(ctx, id)
	if id == b.slice {
		u.ParentID = ""
	}
	return u, err
}

func TestPromptSliceWithoutBrief(t *testing.T) {
	f := newFixture(t)
	deps := f.deps()
	deps.Units = briefless{Service: f.units, slice: f.slice.ID}
	p, err := loop.BuildPrompt(ctx, deps, loop.PromptInput{TaskID: f.task.ID, Attempt: 1, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Text, "Brief ") {
		t.Errorf("brief lines present:\n%s", p.Text)
	}
	if !strings.Contains(p.Text, "Slice "+f.slice.ID+": The slice\n\nThe slice body.") {
		t.Errorf("slice missing:\n%s", p.Text)
	}
}

func TestPromptNoCriteriaSaysNoneRecorded(t *testing.T) {
	f := newFixture(t)
	task := f.unit(t, units.LevelTask, f.slice.ID, "T", "b", "")
	p := f.build(t, loop.PromptInput{TaskID: task.ID})
	if !strings.Contains(p.Text, "## Acceptance criteria\n\n(none recorded)") {
		t.Errorf("text:\n%s", p.Text)
	}
}

func TestPromptNoVerificationCommands(t *testing.T) {
	f := newFixture(t)
	p := f.build(t, loop.PromptInput{})
	if !strings.Contains(p.Text, "## How your work is checked\n\nRalph has no verification commands configured for this project.") {
		t.Errorf("text:\n%s", p.Text)
	}
}

func TestPromptOtherTasksMarkedAndOrdered(t *testing.T) {
	f := newFixture(t)
	f.unit(t, units.LevelTask, f.slice.ID, "Later", "b", "")
	p := f.build(t, loop.PromptInput{})
	want := fmt.Sprintf("- %s PROPOSED Do the thing <- this task\n- T-%d PROPOSED Later", f.task.ID, f.task.Number+1)
	if !strings.Contains(p.Text, want) {
		t.Errorf("want %q in:\n%s", want, p.Text)
	}
}

func TestPromptNotesLastTwentyAndCut(t *testing.T) {
	f := newFixture(t)
	for i := 1; i <= 24; i++ {
		f.note(t, fmt.Sprintf("note-%02d", i))
	}
	f.note(t, strings.Repeat("x", 3000))
	p := f.build(t, loop.PromptInput{})
	if n := strings.Count(p.Text, "\n- NOTE "); n != 20 {
		t.Errorf("notes = %d, want 20", n)
	}
	if strings.Contains(p.Text, "note-05:") || !strings.Contains(p.Text, "note-06") || !strings.Contains(p.Text, "note-24") {
		t.Errorf("wrong notes kept:\n%s", p.Text)
	}
	if !strings.Contains(p.Text, strings.Repeat("x", 2000)+"…\n") || strings.Contains(p.Text, strings.Repeat("x", 2001)) {
		t.Errorf("long note not cut at 2000 with an ellipsis")
	}
}

func ids(p loop.Prompt) []string {
	var out []string
	for _, d := range p.Documents {
		out = append(out, d.ID)
	}
	return out
}

func TestPromptDocumentOrderTaskSliceBrief(t *testing.T) {
	f := newFixture(t)
	b := f.doc(t, "B", "bb", f.brief.ID)
	s := f.doc(t, "S", "ss", f.slice.ID)
	tk := f.doc(t, "T", "tt", f.task.ID)
	p := f.build(t, loop.PromptInput{})
	if got := strings.Join(ids(p), ","); got != strings.Join([]string{tk.ID, s.ID, b.ID}, ",") {
		t.Errorf("order = %s", got)
	}
	for _, want := range []string{
		"### " + tk.ID + " design: T (linked to this task)\n\ntt",
		"### " + s.ID + " design: S (linked to slice " + f.slice.ID + ")\n\nss",
		"### " + b.ID + " design: B (linked to brief " + f.brief.ID + ")\n\nbb",
	} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("missing %q in:\n%s", want, p.Text)
		}
	}
}

func TestPromptDocumentLinkedTwiceOnce(t *testing.T) {
	f := newFixture(t)
	d := f.doc(t, "Both", "text", f.task.ID)
	if err := f.links.Link(ctx, d.ID, f.slice.ID); err != nil {
		t.Fatal(err)
	}
	p := f.build(t, loop.PromptInput{})
	if len(p.Documents) != 1 || strings.Count(p.Text, "### "+d.ID) != 1 || !strings.Contains(p.Text, "(linked to this task)") {
		t.Errorf("docs = %+v\n%s", p.Documents, p.Text)
	}
}

func TestPromptSkipsArchivedSupersededAndMissing(t *testing.T) {
	f := newFixture(t)
	keep := f.doc(t, "Keep", "k", f.task.ID)
	arch := f.doc(t, "Arch", "a", f.task.ID)
	old := f.doc(t, "Old", "o", f.task.ID)
	repl := f.doc(t, "New", "n", "")
	if err := f.units.AddLink(ctx, f.task.ID, units.LinkDocument, "D-999"); err != nil {
		t.Fatal(err)
	}
	a, err := f.docs.Get(ctx, arch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.Archive(ctx, arch.ID, a.Fingerprint); err != nil {
		t.Fatal(err)
	}
	o, err := f.docs.Get(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.Supersede(ctx, old.ID, o.Fingerprint, repl.ID); err != nil {
		t.Fatal(err)
	}
	p := f.build(t, loop.PromptInput{})
	if got := ids(p); len(got) != 1 || got[0] != keep.ID {
		t.Errorf("considered = %v", got)
	}
	for _, bad := range []string{arch.ID + " ", old.ID + " ", "D-999"} {
		if strings.Contains(p.Text, bad) {
			t.Errorf("%q in prompt:\n%s", bad, p.Text)
		}
	}
}

func TestPromptDocumentLimitPointers(t *testing.T) {
	f := newFixture(t)
	near := f.doc(t, "Near", strings.Repeat("a", 40), f.task.ID)
	big := f.doc(t, "Big", strings.Repeat("b", 100), f.slice.ID)
	small := f.doc(t, "Small", strings.Repeat("c", 30), f.brief.ID)
	p := f.build(t, loop.PromptInput{DocumentBytes: 80})
	want := []loop.DocumentUse{
		{ID: near.ID, Title: "Near", Included: true},
		{ID: big.ID, Title: "Big", From: f.slice.ID, Reason: "over the prompt limit"},
		{ID: small.ID, Title: "Small", From: f.brief.ID, Included: true},
	}
	if fmt.Sprint(p.Documents) != fmt.Sprint(want) {
		t.Errorf("documents = %+v", p.Documents)
	}
	ptr := fmt.Sprintf("Not included:\n- %s Big: over the prompt limit. Read it with doc_get.", big.ID)
	if !strings.Contains(p.Text, ptr) || strings.Contains(p.Text, "bbb") {
		t.Errorf("pointer missing:\n%s", p.Text)
	}
	if !strings.Contains(p.Text, "### "+small.ID) {
		t.Errorf("small document missing")
	}
}

func TestPromptDocumentLimitZeroIncludesAll(t *testing.T) {
	f := newFixture(t)
	f.doc(t, "Huge", strings.Repeat("h", 500000), f.task.ID)
	f.doc(t, "Other", "o", f.slice.ID)
	p := f.build(t, loop.PromptInput{})
	for _, d := range p.Documents {
		if !d.Included {
			t.Errorf("%s left out", d.ID)
		}
	}
	if strings.Contains(p.Text, "Not included") {
		t.Error("Not included present")
	}
}

func TestPromptDocumentNeverCut(t *testing.T) {
	f := newFixture(t)
	body := strings.Repeat("0123456789", 20)
	f.doc(t, "Whole", body, f.task.ID)
	fits := f.build(t, loop.PromptInput{DocumentBytes: len(body)})
	if !strings.Contains(fits.Text, body) {
		t.Error("document exactly at the limit not whole")
	}
	over := f.build(t, loop.PromptInput{DocumentBytes: len(body) - 1})
	if strings.Contains(over.Text, body[:50]) {
		t.Error("document partly included")
	}
}

func TestPromptDocumentsReportUse(t *testing.T) {
	f := newFixture(t)
	a := f.doc(t, "A", "aaaa", f.task.ID)
	b := f.doc(t, "B", "bbbbbbbb", f.brief.ID)
	p := f.build(t, loop.PromptInput{DocumentBytes: 5})
	want := []loop.DocumentUse{
		{ID: a.ID, Title: "A", Included: true},
		{ID: b.ID, Title: "B", From: f.brief.ID, Reason: "over the prompt limit"},
	}
	if fmt.Sprint(p.Documents) != fmt.Sprint(want) {
		t.Errorf("documents = %+v", p.Documents)
	}
}

func TestPromptFenceLongerThanBackticksInOutput(t *testing.T) {
	f := newFixture(t)
	p := f.build(t, loop.PromptInput{
		Attempt: 2, MaxAttempts: 3,
		Previous: &loop.Failure{Line: "failed", VerificationOutput: "a ````` b\n```\n"},
	})
	if !strings.Contains(p.Text, "\n``````\na ````` b\n```\n``````\n") {
		t.Errorf("fence wrong:\n%s", p.Text)
	}
}

type failingDocs struct {
	documents.Service
	id string
}

func (d failingDocs) Get(ctx context.Context, id string) (documents.Document, error) {
	if id == d.id {
		return documents.Document{}, errors.New("disk on fire")
	}
	return d.Service.Get(ctx, id)
}

func TestPromptDocumentReadErrorNamesID(t *testing.T) {
	f := newFixture(t)
	d := f.doc(t, "Broken", "x", f.task.ID)
	deps := f.deps()
	deps.Docs = failingDocs{Service: f.docs, id: d.ID}
	_, err := loop.BuildPrompt(ctx, deps, loop.PromptInput{TaskID: f.task.ID, Attempt: 1, MaxAttempts: 1})
	if err == nil || !strings.Contains(err.Error(), d.ID) {
		t.Fatalf("err = %v", err)
	}
}

func TestPromptDeterministic(t *testing.T) {
	f := newFixture(t)
	f.fullScenario(t)
	in := loop.PromptInput{Verify: verifyCmds, Attempt: 2, MaxAttempts: 3, Previous: &loop.Failure{Line: "x", VerificationOutput: "y"}}
	a, b := f.build(t, in), f.build(t, in)
	if a.Text != b.Text || fmt.Sprint(a.Documents) != fmt.Sprint(b.Documents) {
		t.Error("two calls differ")
	}
}

func TestPromptNoRetrySectionOnFirstAttempt(t *testing.T) {
	f := newFixture(t)
	p := f.build(t, loop.PromptInput{Previous: &loop.Failure{Line: "old"}})
	if strings.Contains(p.Text, "previous attempt failed") {
		t.Errorf("retry section on attempt 1:\n%s", p.Text)
	}
	q := f.build(t, loop.PromptInput{Attempt: 2, MaxAttempts: 3})
	if strings.Contains(q.Text, "previous attempt failed") {
		t.Errorf("retry section without a failure")
	}
}
