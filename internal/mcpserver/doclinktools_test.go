package mcpserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// linkFixture is a server over a real store with a brief B-1, a slice S-1 and
// a task T-1 beneath it, and the documents service the server uses.
func linkFixture(t *testing.T) (*mcp.ClientSession, units.Service) {
	t.Helper()
	d := testutil.NewDB(t, db.Migrations)
	us := units.NewService(d)
	cs := connect(t, Options{Name: "ralph", Actor: actor, Open: staticOpen(us), Documents: staticDocs(documents.NewService(d))})
	create(t, cs, map[string]any{"level": "brief", "title": "B", "body": "goal"})
	create(t, cs, map[string]any{"level": "slice", "title": "S", "parent_id": "B-1"})
	create(t, cs, map[string]any{"level": "task", "title": "T", "body": "work", "parent_id": "S-1", "acceptance_criteria": "c"})
	return cs, us
}

func newLinkDoc(t *testing.T, cs *mcp.ClientSession, title string) map[string]any {
	t.Helper()
	return docCreate(t, cs, "design", title, nil)
}

func linkTargets(t *testing.T, got map[string]any) []string {
	t.Helper()
	var out []string
	for _, l := range got["links"].([]any) {
		m := l.(map[string]any)
		out = append(out, m["kind"].(string)+":"+m["target"].(string))
	}
	return out
}

func linkDoc(id, target string) map[string]any {
	return map[string]any{"id": id, "kind": "document", "target": target}
}

func archiveDoc(t *testing.T, cs *mcp.ClientSession, d map[string]any) {
	t.Helper()
	ok(t, cs, "doc_archive", map[string]any{"id": d["id"], "fingerprint": d["fingerprint"]})
}

func TestLinkAddDocumentActive(t *testing.T) {
	cs, _ := linkFixture(t)
	newLinkDoc(t, cs, "Design")
	got := ok(t, cs, "link_add", linkDoc("t-1", "d-1"))
	if g := linkTargets(t, got); len(g) != 1 || g[0] != "document:D-1" {
		t.Errorf("links = %v", g)
	}
}

func TestLinkAddDocumentMissingNotFound(t *testing.T) {
	cs, _ := linkFixture(t)
	fails(t, cs, "link_add", linkDoc("T-1", "D-9"), "not_found")
	if g := ok(t, cs, "unit_get", map[string]any{"id": "T-1"})["links"].([]any); len(g) != 0 {
		t.Errorf("links = %v", g)
	}
}

func TestLinkAddDocumentArchivedInvalid(t *testing.T) {
	cs, _ := linkFixture(t)
	archiveDoc(t, cs, newLinkDoc(t, cs, "Old"))
	res := fails(t, cs, "link_add", linkDoc("T-1", "D-1"), "invalid")
	if !strings.Contains(text(res), "archived") {
		t.Errorf("text = %s", text(res))
	}
}

func TestLinkAddDocumentSupersededNamesReplacement(t *testing.T) {
	cs, _ := linkFixture(t)
	old, repl := newLinkDoc(t, cs, "Old"), newLinkDoc(t, cs, "New")
	ok(t, cs, "doc_supersede", map[string]any{"id": old["id"], "fingerprint": old["fingerprint"], "superseded_by": repl["id"]})
	res := fails(t, cs, "link_add", linkDoc("T-1", "D-1"), "invalid")
	if !strings.Contains(text(res), "superseded by D-2") {
		t.Errorf("text = %s", text(res))
	}
}

func TestLinkAddDocumentMalformedInvalid(t *testing.T) {
	cs, _ := linkFixture(t)
	for _, target := range []string{"X-1", "D-01", "D-", "has space", ""} {
		fails(t, cs, "link_add", linkDoc("T-1", target), "invalid")
	}
}

func TestLinkAddDocumentTwiceChangesNothing(t *testing.T) {
	cs, _ := linkFixture(t)
	newLinkDoc(t, cs, "Design")
	ok(t, cs, "link_add", linkDoc("T-1", "D-1"))
	before := ok(t, cs, "unit_get", map[string]any{"id": "T-1"})["fingerprint"]
	got := ok(t, cs, "link_add", linkDoc("T-1", "d-1"))
	if g := linkTargets(t, got); len(g) != 1 {
		t.Errorf("links = %v", g)
	}
	if after := ok(t, cs, "unit_get", map[string]any{"id": "T-1"})["fingerprint"]; after != before {
		t.Errorf("fingerprint changed: %v -> %v", before, after)
	}
}

func TestLinkAddDocumentUnknownUnitNotFound(t *testing.T) {
	cs, _ := linkFixture(t)
	newLinkDoc(t, cs, "Design")
	fails(t, cs, "link_add", linkDoc("T-9", "D-1"), "not_found")
}

func TestLinkRemoveDocumentAnyStatus(t *testing.T) {
	cs, us := linkFixture(t)
	d := newLinkDoc(t, cs, "Old")
	ok(t, cs, "link_add", linkDoc("T-1", "D-1"))
	archiveDoc(t, cs, d)
	if g := linkTargets(t, ok(t, cs, "link_remove", linkDoc("T-1", "D-1"))); len(g) != 0 {
		t.Errorf("links after removing the archived one = %v", g)
	}

	if err := us.AddLink(context.Background(), "T-1", units.LinkDocument, "D-9"); err != nil {
		t.Fatal(err)
	}
	if g := linkTargets(t, ok(t, cs, "link_remove", linkDoc("T-1", "d-9"))); len(g) != 0 {
		t.Errorf("links after removing the missing one = %v", g)
	}
}

func TestLinkRemoveDocumentNotThereIsFine(t *testing.T) {
	cs, _ := linkFixture(t)
	for _, target := range []string{"D-1", "d-7"} {
		if g := linkTargets(t, ok(t, cs, "link_remove", linkDoc("T-1", target))); len(g) != 0 {
			t.Errorf("links = %v", g)
		}
	}
	fails(t, cs, "link_remove", linkDoc("T-1", "nonsense"), "invalid")
}

func TestLinkExternalUnchangedAndDoesNotOpenDocuments(t *testing.T) {
	d := testutil.NewDB(t, db.Migrations)
	opened := false
	us := units.NewService(d)
	cs := connect(t, Options{Name: "ralph", Actor: actor, Open: staticOpen(us),
		Documents: func(context.Context) (documents.Service, error) {
			opened = true
			return nil, errors.New("no documents")
		}})
	if _, err := us.Create(context.Background(), units.NewUnit{Level: units.LevelBrief, Title: "B", Body: "goal"}); err != nil {
		t.Fatal(err)
	}
	ext := map[string]any{"id": "B-1", "kind": "external", "target": "JIRA-1"}
	if g := linkTargets(t, ok(t, cs, "link_add", ext)); len(g) != 1 || g[0] != "external:JIRA-1" {
		t.Errorf("links = %v", g)
	}
	if g := linkTargets(t, ok(t, cs, "link_remove", ext)); len(g) != 0 {
		t.Errorf("links = %v", g)
	}
	if opened {
		t.Error("the documents service was opened for an external link")
	}
	fails(t, cs, "link_add", linkDoc("B-1", "D-1"), "internal")
	if !opened {
		t.Error("a document link did not try the documents service")
	}
}

func TestLinkToolDescriptionsMentionActiveDocuments(t *testing.T) {
	cs, _ := newDocClient(t)
	got := tools(t, cs)
	add := got["link_add"].Description
	for _, want := range []string{"ACTIVE", "not_found", "invalid", "superseded", "archived"} {
		if !strings.Contains(add, want) {
			t.Errorf("link_add description lacks %q", want)
		}
	}
	rm := got["link_remove"].Description
	if !strings.Contains(rm, "any status") || !strings.Contains(rm, "no longer exists") {
		t.Errorf("link_remove description = %s", rm)
	}
}

func TestUnitGetShowsDirectAndInheritedDocuments(t *testing.T) {
	cs, _ := linkFixture(t)
	newLinkDoc(t, cs, "Own")
	newLinkDoc(t, cs, "Above")
	ok(t, cs, "link_add", linkDoc("T-1", "D-1"))
	ok(t, cs, "link_add", linkDoc("S-1", "D-2"))
	res := call(t, cs, "unit_get", map[string]any{"id": "T-1"})
	got := res.StructuredContent.(map[string]any)
	direct, inherited := got["documents"].([]any), got["inherited_documents"].([]any)
	if len(direct) != 1 || direct[0].(map[string]any)["id"] != "D-1" || direct[0].(map[string]any)["title"] != "Own" {
		t.Errorf("documents = %v", direct)
	}
	if len(inherited) != 1 || inherited[0].(map[string]any)["id"] != "D-2" || inherited[0].(map[string]any)["from"] != "S-1" {
		t.Errorf("inherited = %v", inherited)
	}
	for _, want := range []string{"document: D-1 design ACTIVE Own", "document (from S-1): D-2 design ACTIVE Above"} {
		if !strings.Contains(text(res), want) {
			t.Errorf("text lacks %q:\n%s", want, text(res))
		}
	}
}

func TestUnitGetDocumentsEmptyArrays(t *testing.T) {
	cs, _ := linkFixture(t)
	got := ok(t, cs, "unit_get", map[string]any{"id": "T-1"})
	for _, k := range []string{"documents", "inherited_documents"} {
		list, isList := got[k].([]any)
		if !isList || len(list) != 0 {
			t.Errorf("%s = %#v", k, got[k])
		}
	}
}

func TestUnitGetSupersededAndMissingText(t *testing.T) {
	cs, us := linkFixture(t)
	old, repl := newLinkDoc(t, cs, "Old"), newLinkDoc(t, cs, "New")
	ok(t, cs, "link_add", linkDoc("T-1", "D-1"))
	ok(t, cs, "link_add", linkDoc("T-1", "D-2"))
	ok(t, cs, "doc_supersede", map[string]any{"id": old["id"], "fingerprint": old["fingerprint"], "superseded_by": repl["id"]})
	if err := us.AddLink(context.Background(), "T-1", units.LinkDocument, "D-9"); err != nil {
		t.Fatal(err)
	}
	res := call(t, cs, "unit_get", map[string]any{"id": "T-1"})
	for _, want := range []string{"document: D-1 design SUPERSEDED by D-2 Old", "document: D-2 design ACTIVE New", "document: D-9 MISSING"} {
		if !strings.Contains(text(res), want) {
			t.Errorf("text lacks %q:\n%s", want, text(res))
		}
	}
}

func TestUnitGetWithoutDocumentsServiceGivesNoticeAndEmptyFields(t *testing.T) {
	d := testutil.NewDB(t, db.Migrations)
	us := units.NewService(d)
	cs := connect(t, Options{Name: "ralph", Actor: actor, Open: staticOpen(us),
		Documents: func(context.Context) (documents.Service, error) { return nil, errors.New("disk on fire") }})
	if _, err := us.Create(context.Background(), units.NewUnit{Level: units.LevelBrief, Title: "B", Body: "goal"}); err != nil {
		t.Fatal(err)
	}
	res := call(t, cs, "unit_get", map[string]any{"id": "B-1"})
	if res.IsError {
		t.Fatalf("error: %s", text(res))
	}
	got := res.StructuredContent.(map[string]any)
	if len(got["documents"].([]any)) != 0 || len(got["inherited_documents"].([]any)) != 0 {
		t.Errorf("got = %v", got)
	}
	notices := got["notices"].([]any)
	if len(notices) != 1 || !strings.Contains(notices[0].(map[string]any)["message"].(string), "disk on fire") {
		t.Errorf("notices = %v", notices)
	}
}

func TestUnitWriteToolsIncludeDocumentFields(t *testing.T) {
	cs, _ := linkFixture(t)
	newLinkDoc(t, cs, "Own")
	newLinkDoc(t, cs, "Above")
	ok(t, cs, "link_add", linkDoc("T-1", "D-1"))
	ok(t, cs, "link_add", linkDoc("S-1", "D-2"))
	check := func(name string, got map[string]any) {
		t.Helper()
		if len(got["documents"].([]any)) != 1 || len(got["inherited_documents"].([]any)) != 1 {
			t.Errorf("%s: documents = %v, inherited = %v", name, got["documents"], got["inherited_documents"])
		}
	}
	cur := ok(t, cs, "unit_get", map[string]any{"id": "T-1"})
	upd := ok(t, cs, "unit_update", map[string]any{"id": "T-1", "fingerprint": cur["fingerprint"], "title": "T2"})
	check("unit_update", upd)
	set := ok(t, cs, "task_set_state", map[string]any{"id": "T-1", "fingerprint": upd["fingerprint"], "state": "TODO"})
	check("task_set_state", set)
	made := create(t, cs, map[string]any{"level": "task", "title": "new", "body": "work", "parent_id": "S-1", "acceptance_criteria": "c"})
	if len(made["documents"].([]any)) != 0 || len(made["inherited_documents"].([]any)) != 1 {
		t.Errorf("unit_create: %v", made)
	}
}

func TestDocGetLinkedUnits(t *testing.T) {
	cs, _ := linkFixture(t)
	newLinkDoc(t, cs, "Design")
	newLinkDoc(t, cs, "Alone")
	ok(t, cs, "link_add", linkDoc("T-1", "D-1"))
	ok(t, cs, "link_add", linkDoc("B-1", "D-1"))
	res := call(t, cs, "doc_get", map[string]any{"id": "d-1"})
	got := res.StructuredContent.(map[string]any)
	linked := got["linked_units"].([]any)
	if len(linked) != 2 || linked[0].(map[string]any)["id"] != "B-1" || linked[0].(map[string]any)["level"] != "brief" || linked[1].(map[string]any)["title"] != "T" {
		t.Errorf("linked_units = %v", linked)
	}
	for _, want := range []string{"linked: B-1 brief B", "linked: T-1 task T"} {
		if !strings.Contains(text(res), want) {
			t.Errorf("text lacks %q:\n%s", want, text(res))
		}
	}
	alone := call(t, cs, "doc_get", map[string]any{"id": "D-2"})
	list, isList := alone.StructuredContent.(map[string]any)["linked_units"].([]any)
	if !isList || len(list) != 0 || strings.Contains(text(alone), "linked:") {
		t.Errorf("alone = %#v, text %s", alone.StructuredContent, text(alone))
	}
	ver := ok(t, cs, "doc_get", map[string]any{"id": "D-1", "version": 1})
	if l, isList := ver["linked_units"].([]any); !isList || len(l) != 2 {
		t.Errorf("version read linked_units = %v", ver["linked_units"])
	}
}

func TestDocGetLinkedUnitsAfterLinkAndUnlink(t *testing.T) {
	cs, _ := linkFixture(t)
	newLinkDoc(t, cs, "Design")
	count := func() int {
		return len(ok(t, cs, "doc_get", map[string]any{"id": "D-1"})["linked_units"].([]any))
	}
	if count() != 0 {
		t.Fatal("linked before linking")
	}
	ok(t, cs, "link_add", linkDoc("T-1", "D-1"))
	if count() != 1 {
		t.Error("not linked after link_add")
	}
	ok(t, cs, "link_remove", linkDoc("T-1", "D-1"))
	if count() != 0 {
		t.Error("still linked after link_remove")
	}
}

func TestLinkToolsDocumentSurfaces(t *testing.T) {
	for name, fn := range map[string]func(*testing.T){
		"LinkAddDocumentActive":                          TestLinkAddDocumentActive,
		"LinkAddDocumentMissingNotFound":                 TestLinkAddDocumentMissingNotFound,
		"LinkAddDocumentArchivedInvalid":                 TestLinkAddDocumentArchivedInvalid,
		"LinkAddDocumentSupersededNamesReplacement":      TestLinkAddDocumentSupersededNamesReplacement,
		"LinkAddDocumentMalformedInvalid":                TestLinkAddDocumentMalformedInvalid,
		"LinkAddDocumentTwiceChangesNothing":             TestLinkAddDocumentTwiceChangesNothing,
		"LinkRemoveDocumentAnyStatus":                    TestLinkRemoveDocumentAnyStatus,
		"LinkRemoveDocumentNotThereIsFine":               TestLinkRemoveDocumentNotThereIsFine,
		"LinkExternalUnchangedAndDoesNotOpenDocuments":   TestLinkExternalUnchangedAndDoesNotOpenDocuments,
		"LinkToolDescriptionsMentionActiveDocuments":     TestLinkToolDescriptionsMentionActiveDocuments,
		"UnitGetShowsDirectAndInheritedDocuments":        TestUnitGetShowsDirectAndInheritedDocuments,
		"UnitGetSupersededAndMissingText":                TestUnitGetSupersededAndMissingText,
		"UnitGetWithoutDocumentsServiceGivesNoticeEmpty": TestUnitGetWithoutDocumentsServiceGivesNoticeAndEmptyFields,
		"UnitWriteToolsIncludeDocumentFields":            TestUnitWriteToolsIncludeDocumentFields,
	} {
		t.Run(name, fn)
	}
}
