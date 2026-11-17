package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/units"
)

type linkSeed struct {
	brief, slice, task string
	d1, d2, d3         string
}

// seedLinks makes B-1, S-1, T-1 and three design documents.
func (e *docsEnv) seedLinks() linkSeed {
	e.t.Helper()
	var s linkSeed
	e.with(func(d *db.DB, docs documents.Service) {
		u := units.NewService(d)
		mk := func(level units.Level, parent, title string) string {
			x, err := u.Create(e.ctx, units.NewUnit{Level: level, ParentID: parent, Title: title, Body: "b"})
			if err != nil {
				e.t.Fatal(err)
			}
			return x.ID
		}
		s.brief = mk(units.LevelBrief, "", "Brief")
		s.slice = mk(units.LevelSlice, s.brief, "Slice")
		s.task = mk(units.LevelTask, s.slice, "Task title")
		doc := func(title string) documents.Document {
			x, err := docs.Create(e.ctx, documents.NewDocument{Kind: documents.KindDesign, Title: title, Body: "b"})
			if err != nil {
				e.t.Fatal(err)
			}
			return x
		}
		d1, d2, d3 := doc("Pull requests and naming"), doc("Second"), doc("Third")
		s.d1, s.d2, s.d3 = d1.ID, d2.ID, d3.ID
	})
	return s
}

func TestDocsLinkPrintsAndLinks(t *testing.T) {
	e := newDocsEnv(t)
	s := e.seedLinks()
	code, out, stderr := run(t, "docs", "link", strings.ToLower(s.d1), strings.ToLower(s.task))
	if code != 0 || out != s.d1+" is linked to "+s.task+"\n" || stderr != "" {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
	code, out, _ = run(t, "docs", "links", s.task)
	if code != 0 || out != "Documents:\n"+s.d1+"  design  ACTIVE  Pull requests and naming\n" {
		t.Fatalf("code %d out %q", code, out)
	}
}

func TestDocsLinkTwiceSucceeds(t *testing.T) {
	e := newDocsEnv(t)
	s := e.seedLinks()
	for i := 0; i < 2; i++ {
		if code, _, stderr := run(t, "docs", "link", s.d1, s.task); code != 0 {
			t.Fatalf("run %d: code %d stderr %q", i, code, stderr)
		}
	}
}

func TestDocsUnlinkPrintsAndUnlinks(t *testing.T) {
	e := newDocsEnv(t)
	s := e.seedLinks()
	run(t, "docs", "link", s.d1, s.task)
	for i := 0; i < 2; i++ {
		code, out, stderr := run(t, "docs", "unlink", s.d1, s.task)
		if code != 0 || out != s.d1+" is not linked to "+s.task+"\n" || stderr != "" {
			t.Fatalf("run %d: code %d out %q stderr %q", i, code, out, stderr)
		}
	}
	if _, out, _ := run(t, "docs", "links", s.task); out != s.task+" has no linked documents\n" {
		t.Fatalf("out %q", out)
	}
}

func TestDocsLinkErrorsExitOne(t *testing.T) {
	e := newDocsEnv(t)
	s := e.seedLinks()
	e.with(func(_ *db.DB, docs documents.Service) {
		d, err := docs.Get(e.ctx, s.d2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := docs.Archive(e.ctx, d.ID, d.Fingerprint); err != nil {
			t.Fatal(err)
		}
	})
	for _, c := range []struct{ doc, unit, want string }{
		{s.d2, s.task, "is archived and cannot be linked"},
		{"D-99", s.task, "document D-99 was not found"},
		{"D-03", s.task, `"D-03"`},
		{s.d1, "T-99", "T-99"},
	} {
		code, out, stderr := run(t, "docs", "link", c.doc, c.unit)
		if code != 1 || out != "" || !strings.HasPrefix(stderr, "ralph: ") || !strings.Contains(stderr, c.want) {
			t.Errorf("%s %s: code %d out %q stderr %q", c.doc, c.unit, code, out, stderr)
		}
	}
}

func TestDocsLinksForDocument(t *testing.T) {
	e := newDocsEnv(t)
	s := e.seedLinks()
	if code, out, _ := run(t, "docs", "links", s.d1); code != 0 || out != "no units are linked to "+s.d1+"\n" {
		t.Fatalf("code %d out %q", code, out)
	}
	run(t, "docs", "link", s.d1, s.task)
	run(t, "docs", "link", s.d1, s.brief)
	code, out, _ := run(t, "docs", "links", s.d1)
	want := s.brief + "  brief  Brief\n" + s.task + "  task   Task title\n"
	if code != 0 || out != want {
		t.Fatalf("code %d out %q want %q", code, out, want)
	}
	code, out, _ = run(t, "docs", "links", s.d1, "--json")
	var got struct {
		ID    string
		Units []struct{ ID, Level, Title string }
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 || got.ID != s.d1 || len(got.Units) != 2 || got.Units[1].Level != "task" {
		t.Fatalf("code %d out %q err %v", code, out, err)
	}
	_, out, _ = run(t, "docs", "links", s.d2, "--json")
	if !strings.Contains(out, `"units": []`) {
		t.Fatalf("out %q", out)
	}
}

func TestDocsLinksForUnitBlocks(t *testing.T) {
	e := newDocsEnv(t)
	s := e.seedLinks()
	var repl string
	e.with(func(_ *db.DB, docs documents.Service) {
		r, err := docs.Create(e.ctx, documents.NewDocument{Kind: documents.KindDesign, Title: "New", Body: "b"})
		if err != nil {
			t.Fatal(err)
		}
		repl = r.ID
	})
	run(t, "docs", "link", s.d1, s.task)
	run(t, "docs", "link", s.d3, s.slice)
	run(t, "docs", "link", s.d2, s.brief)
	e.with(func(d *db.DB, docs documents.Service) {
		x, err := docs.Get(e.ctx, s.d3)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := docs.Supersede(e.ctx, x.ID, x.Fingerprint, repl); err != nil {
			t.Fatal(err)
		}
		if err := units.NewService(d).AddLink(e.ctx, s.brief, units.LinkDocument, "D-99"); err != nil {
			t.Fatal(err)
		}
	})
	code, out, _ := run(t, "docs", "links", s.task)
	want := "Documents:\n" + s.d1 + "  design  ACTIVE  Pull requests and naming\n" +
		"Documents from " + s.slice + ":\n" + s.d3 + "  design  SUPERSEDED (replaced by " + repl + ")  Third\n" +
		"Documents from " + s.brief + ":\n" + s.d2 + "  design  ACTIVE  Second\nD-99  MISSING\n"
	if code != 0 || out != want {
		t.Fatalf("code %d out\n%s\nwant\n%s", code, out, want)
	}
	code, out, _ = run(t, "docs", "links", s.task, "--json")
	var got struct {
		ID        string
		Documents []struct{ ID string }
		Inherited []struct{ ID, From string } `json:"inherited_documents"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 || len(got.Documents) != 1 || len(got.Inherited) != 3 || got.Inherited[0].From != s.slice {
		t.Fatalf("code %d out %q err %v", code, out, err)
	}
}

func TestDocsLinksForUnitWithNone(t *testing.T) {
	e := newDocsEnv(t)
	s := e.seedLinks()
	if code, out, _ := run(t, "docs", "links", strings.ToLower(s.task)); code != 0 || out != s.task+" has no linked documents\n" {
		t.Fatalf("code %d out %q", code, out)
	}
	_, out, _ := run(t, "docs", "links", s.task, "--json")
	if !strings.Contains(out, `"documents": []`) || !strings.Contains(out, `"inherited_documents": []`) {
		t.Fatalf("out %q", out)
	}
}

func TestDocsLinksBadID(t *testing.T) {
	newDocsEnv(t)
	code, out, stderr := run(t, "docs", "links", "X-1")
	if code != 1 || out != "" || !strings.Contains(stderr, "document ID") || !strings.Contains(stderr, "unit ID") {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
}

func TestDocsLinkNotInitialized(t *testing.T) {
	inRepo(t)
	for _, args := range [][]string{{"docs", "link", "D-1", "T-1"}, {"docs", "unlink", "D-1", "T-1"}, {"docs", "links", "D-1"}} {
		code, _, stderr := run(t, args...)
		if code != 1 || !strings.Contains(stderr, "ralph init") {
			t.Errorf("%v: code %d stderr %q", args, code, stderr)
		}
	}
}

func TestDocsHelpListsLinkCommands(t *testing.T) {
	code, out, _ := run(t, "docs", "--help")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, c := range []string{"link ", "unlink", "links"} {
		if !strings.Contains(out, c) {
			t.Errorf("help lacks %q: %s", c, out)
		}
	}
}
