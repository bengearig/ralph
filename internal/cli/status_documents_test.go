package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/doclinks"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/units"
)

type statusDocs struct {
	brief, slice, task string
	d1, d2, d3         string
}

func (e *statusEnv) seedDocs() statusDocs {
	e.t.Helper()
	var s statusDocs
	d, err := db.Open(e.ctx, filepath.Join(e.repo.Dir, ".ralph", "store.db"), db.Options{})
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	us, docs := units.NewService(d), documents.NewService(d)
	s.brief = e.create(us, units.LevelBrief, "", "Brief", "")
	s.slice = e.create(us, units.LevelSlice, s.brief, "Slice", "")
	s.task = e.create(us, units.LevelTask, s.slice, "Task", "")
	mk := func(title string) documents.Document {
		x, err := docs.Create(e.ctx, documents.NewDocument{Kind: documents.KindDesign, Title: title, Body: "b"})
		if err != nil {
			e.t.Fatal(err)
		}
		return x
	}
	x1, x2, x3 := mk("First"), mk("Second"), mk("Third")
	s.d1, s.d2, s.d3 = x1.ID, x2.ID, x3.ID
	return s
}

func (e *statusEnv) link(unit, kind, target string) {
	e.t.Helper()
	e.with(func(svc units.Service) {
		if err := svc.AddLink(e.ctx, unit, units.LinkKind(kind), target); err != nil {
			e.t.Fatal(err)
		}
	})
}

func TestStatusUnitShowsDirectDocuments(t *testing.T) {
	e := newStatusEnv(t)
	s := e.seedDocs()
	e.link(s.task, "document", s.d1)
	code, out, stderr := e.status(s.task)
	if code != 0 || stderr != "" {
		t.Fatalf("code %d %q", code, stderr)
	}
	want := "\nDocuments:\n" + s.d1 + "  design  ACTIVE  First\n"
	if !strings.Contains(out, want) {
		t.Fatalf("missing %q in:\n%s", want, out)
	}
}

func TestStatusUnitShowsInheritedDocumentsPerAncestor(t *testing.T) {
	e := newStatusEnv(t)
	s := e.seedDocs()
	e.link(s.task, "document", s.d1)
	e.link(s.slice, "document", s.d2)
	e.link(s.brief, "document", s.d3)
	_, out, _ := e.status(s.task)
	want := "\nDocuments:\n" + s.d1 + "  design  ACTIVE  First\n" +
		"Documents from " + s.slice + ":\n" + s.d2 + "  design  ACTIVE  Second\n" +
		"Documents from " + s.brief + ":\n" + s.d3 + "  design  ACTIVE  Third\n"
	if !strings.Contains(out, want) {
		t.Fatalf("missing %q in:\n%s", want, out)
	}
}

func TestStatusUnitDocumentsBeforeNotes(t *testing.T) {
	e := newStatusEnv(t)
	s := e.seedDocs()
	e.link(s.task, "external", "https://example.com")
	e.link(s.task, "document", s.d1)
	e.with(func(svc units.Service) {
		if _, err := svc.AddNote(e.ctx, s.task, units.NoteGeneral, "a note"); err != nil {
			t.Fatal(err)
		}
	})
	_, out, _ := e.status(s.task)
	l, d, n := strings.Index(out, "\nLinks:\n"), strings.Index(out, "\nDocuments:\n"), strings.Index(out, "\nNotes:\n")
	if l < 0 || d < 0 || n < 0 || l >= d || d >= n {
		t.Fatalf("order wrong (links %d, documents %d, notes %d):\n%s", l, d, n, out)
	}
}

func TestStatusUnitSupersededAndMissingLines(t *testing.T) {
	e := newStatusEnv(t)
	s := e.seedDocs()
	d, err := db.Open(e.ctx, filepath.Join(e.repo.Dir, ".ralph", "store.db"), db.Options{})
	if err != nil {
		t.Fatal(err)
	}
	docs := documents.NewService(d)
	old, err := docs.Get(e.ctx, s.d1)
	if err == nil {
		_, err = docs.Supersede(e.ctx, s.d1, old.Fingerprint, s.d2)
	}
	_ = d.Close()
	if err != nil {
		t.Fatal(err)
	}
	e.link(s.task, "document", s.d1)
	e.link(s.task, "document", "D-99")
	_, out, _ := e.status(s.task)
	for _, want := range []string{
		s.d1 + "  design  SUPERSEDED (replaced by " + s.d2 + ")  First\n",
		"D-99  MISSING\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestStatusUnitDocumentLinksNotInLinksBlock(t *testing.T) {
	e := newStatusEnv(t)
	s := e.seedDocs()
	e.link(s.task, "document", s.d1)
	_, out, _ := e.status(s.task)
	if strings.Contains(out, "Links:") || strings.Contains(out, "document "+s.d1) {
		t.Fatalf("document link in Links block:\n%s", out)
	}
	e.link(s.task, "external", "https://example.com")
	_, out, _ = e.status(s.task)
	if !strings.Contains(out, "\nLinks:\nexternal https://example.com\n") || strings.Contains(out, "document "+s.d1) {
		t.Fatalf("unexpected Links block:\n%s", out)
	}
}

func TestStatusUnitJSONHasDocumentFields(t *testing.T) {
	e := newStatusEnv(t)
	s := e.seedDocs()
	e.link(s.task, "document", s.d1)
	_, out, _ := e.status(s.task, "--json")
	var got struct {
		Links              []map[string]string `json:"links"`
		Documents          []json.RawMessage   `json:"documents"`
		InheritedDocuments []json.RawMessage   `json:"inherited_documents"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Documents == nil || got.InheritedDocuments == nil || len(got.Documents) != 1 || len(got.InheritedDocuments) != 0 {
		t.Fatalf("documents %v inherited %v", got.Documents, got.InheritedDocuments)
	}
	if len(got.Links) != 1 || got.Links[0]["kind"] != "document" || got.Links[0]["target"] != s.d1 {
		t.Fatalf("links = %v", got.Links)
	}
}

var stampLine = regexp.MustCompile(`(?m)^(created|updated): .*$`)

func TestStatusUnitWithoutLinksUnchanged(t *testing.T) {
	e := newStatusEnv(t)
	s := e.seedDocs()
	e.with(func(svc units.Service) {
		if _, err := svc.AddNote(e.ctx, s.task, units.NoteGeneral, "a note"); err != nil {
			t.Fatal(err)
		}
	})
	e.link(s.task, "external", "https://example.com")
	_, out, _ := e.status(s.task)
	golden := "T-1  PROPOSED  Task\nparent: S-1 Slice\ncreated: X\nupdated: X\n\nbody of Task\n\n" +
		"Acceptance criteria:\ncriteria of Task\n\nLinks:\nexternal https://example.com\n\nNotes:\nX\n  a note\n"
	got := stampLine.ReplaceAllString(out, "$1: X")
	got = regexp.MustCompile(`(?m)^\S+ by .*$`).ReplaceAllString(got, "X")
	if got != golden {
		t.Fatalf("got:\n%q\nwant:\n%q", got, golden)
	}
}

func TestStatusTreeUnchangedWithLinks(t *testing.T) {
	e := newStatusEnv(t)
	s := e.seedDocs()
	for _, args := range [][]string{{}, {"--all"}} {
		_, before, _ := e.status(args...)
		e.link(s.task, "document", s.d1)
		e.link(s.brief, "document", s.d2)
		_, after, _ := e.status(args...)
		if before != after {
			t.Fatalf("overview changed:\n%s\n---\n%s", before, after)
		}
	}
}

type failingDocs struct{ documents.Service }

func (failingDocs) Get(context.Context, string) (documents.Document, error) {
	return documents.Document{}, errors.New("disk on fire")
}

func TestStatusUnitDocumentsUnreadableWarnsAndSucceeds(t *testing.T) {
	e := newStatusEnv(t)
	s := e.seedDocs()
	e.link(s.task, "document", s.d1)
	d, err := db.Open(e.ctx, filepath.Join(e.repo.Dir, ".ralph", "store.db"), db.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	us := units.NewService(d)
	var out, errOut bytes.Buffer
	links := doclinks.New(us, failingDocs{documents.NewService(d)})
	if err := statusUnit(e.ctx, &out, &errOut, us, links, s.task, false); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), s.task+"  PROPOSED  Task\n") || strings.Contains(out.String(), "Documents:") {
		t.Fatalf("out:\n%s", out.String())
	}
	if want := "ralph: warning: could not read linked documents: "; !strings.HasPrefix(errOut.String(), want) || strings.Count(errOut.String(), "\n") != 1 {
		t.Fatalf("stderr %q", errOut.String())
	}
}
