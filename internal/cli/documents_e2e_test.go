package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func e2eDocIDs(t *testing.T, cs *mcp.ClientSession, args map[string]any) []string {
	t.Helper()
	listed := e2eOK(t, cs, "doc_list", args)
	docs, _ := listed["documents"].([]any)
	ids := []string{}
	for _, d := range docs {
		ids = append(ids, d.(map[string]any)["id"].(string))
	}
	return ids
}

func e2eWantIDs(t *testing.T, label string, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

func e2eVersionNumbers(t *testing.T, cs *mcp.ClientSession, id string) []int {
	t.Helper()
	res := e2eOK(t, cs, "doc_versions", map[string]any{"id": id})
	list, _ := res["versions"].([]any)
	nums := []int{}
	for _, v := range list {
		nums = append(nums, int(v.(map[string]any)["version"].(float64)))
	}
	return nums
}

func e2eWantVersions(t *testing.T, cs *mcp.ClientSession, id string, want ...int) {
	t.Helper()
	got := e2eVersionNumbers(t, cs, id)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s earlier versions = %v, want %v", id, got, want)
	}
}

// e2eEditTimes edits a document n times and returns its last fingerprint.
func e2eEditTimes(t *testing.T, cs *mcp.ClientSession, id string, n int) string {
	t.Helper()
	fp := e2eOK(t, cs, "doc_get", map[string]any{"id": id})["fingerprint"].(string)
	for i := 1; i <= n; i++ {
		out := e2eOK(t, cs, "doc_update", map[string]any{"id": id, "fingerprint": fp, "body": fmt.Sprintf("body %d", i)})
		fp = out["fingerprint"].(string)
	}
	return fp
}

type e2eRun struct {
	code           int
	stdout, stderr string
}

func (e *e2eEnv) run(t *testing.T, args ...string) e2eRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := e.command(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r := e2eRun{stdout: stdout.String(), stderr: stderr.String()}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		r.code = exit.ExitCode()
	default:
		t.Fatalf("ralph %v: %v", args, err)
	}
	return r
}

func TestE2EDocumentsFlow(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)

	brief := e2eOK(t, cs, "unit_create", map[string]any{"level": "brief", "title": "Ship it", "body": "Please ship it."})
	briefID := brief["id"].(string)
	slice := e2eOK(t, cs, "unit_create", map[string]any{"level": "slice", "parent_id": briefID, "title": "A slice"})
	sliceID := slice["id"].(string)

	wide := e2eOK(t, cs, "doc_create", map[string]any{"kind": "architecture", "title": "Overview", "body": "The shape."})
	tied := e2eOK(t, cs, "doc_create", map[string]any{"kind": "decision", "title": "Pick", "body": "We pick.", "brief_id": briefID})
	wideID, tiedID := wide["id"].(string), tied["id"].(string)
	if wideID != "D-1" || tiedID != "D-2" {
		t.Fatalf("ids = %s, %s", wideID, tiedID)
	}
	if wide["brief_id"] != "" || tied["brief_id"] != briefID || wide["status"] != "ACTIVE" || wide["version"] != float64(1) {
		t.Errorf("created = %v, %v", wide, tied)
	}

	got := e2eOK(t, cs, "doc_get", map[string]any{"id": wideID})
	if got["title"] != "Overview" || got["body"] != "The shape." || got["fingerprint"] != wide["fingerprint"] {
		t.Errorf("doc_get wide = %v", got)
	}
	got = e2eOK(t, cs, "doc_get", map[string]any{"id": tiedID})
	if got["brief_id"] != briefID || got["fingerprint"] != tied["fingerprint"] {
		t.Errorf("doc_get tied = %v", got)
	}

	e2eWantIDs(t, "default list", e2eDocIDs(t, cs, nil), "D-1", "D-2")
	e2eWantIDs(t, "project only", e2eDocIDs(t, cs, map[string]any{"project_only": true}), "D-1")
	e2eWantIDs(t, "brief", e2eDocIDs(t, cs, map[string]any{"brief_id": briefID}), "D-2")
	e2eWantIDs(t, "kind", e2eDocIDs(t, cs, map[string]any{"kinds": []string{"decision"}}), "D-2")

	oldFP := wide["fingerprint"].(string)
	upd := e2eOK(t, cs, "doc_update", map[string]any{"id": wideID, "fingerprint": oldFP, "body": "The new shape."})
	newFP := upd["fingerprint"].(string)
	if newFP == oldFP || upd["version"] != float64(2) || upd["body"] != "The new shape." {
		t.Errorf("doc_update = %v", upd)
	}
	e2eWantVersions(t, cs, wideID, 1)
	vers := e2eOK(t, cs, "doc_versions", map[string]any{"id": wideID})
	if vers["current"] != float64(2) {
		t.Errorf("doc_versions current = %v", vers["current"])
	}
	old := e2eOK(t, cs, "doc_get", map[string]any{"id": wideID, "version": 1})
	if old["body"] != "The shape." || old["version"] != float64(1) {
		t.Errorf("doc_get version 1 = %v", old)
	}

	res := e2eCall(t, cs, "doc_update", map[string]any{"id": wideID, "fingerprint": oldFP, "body": "Theirs"})
	if code := e2eErrorCode(t, res); code != "stale" {
		t.Fatalf("error_code = %q, want stale (%s)", code, e2eText(res))
	}
	if !strings.Contains(e2eText(res), "doc_get") {
		t.Errorf("stale message does not name doc_get: %s", e2eText(res))
	}
	if cur := e2eOK(t, cs, "doc_get", map[string]any{"id": wideID}); cur["body"] != "The new shape." {
		t.Errorf("body after stale = %v", cur["body"])
	}

	same := e2eOK(t, cs, "doc_update", map[string]any{"id": wideID, "fingerprint": newFP, "body": "The new shape."})
	if same["fingerprint"] != newFP || same["version"] != float64(2) {
		t.Errorf("no-op edit = %v, want fingerprint %s at version 2", same, newFP)
	}
	e2eWantVersions(t, cs, wideID, 1)

	res = e2eCall(t, cs, "doc_create", map[string]any{"kind": "design", "title": "Bad", "body": "x", "brief_id": sliceID})
	if code := e2eErrorCode(t, res); code != "invalid" {
		t.Errorf("doc_create tied to a slice: error_code = %q (%s)", code, e2eText(res))
	}

	arch := e2eOK(t, cs, "doc_archive", map[string]any{"id": tiedID, "fingerprint": tied["fingerprint"]})
	if arch["status"] != "ARCHIVED" {
		t.Fatalf("doc_archive = %v", arch)
	}
	e2eWantIDs(t, "list after archive", e2eDocIDs(t, cs, nil), "D-1")
	e2eWantIDs(t, "archived list", e2eDocIDs(t, cs, map[string]any{"statuses": []string{"ARCHIVED"}}), "D-2")
	e2eWantIDs(t, "all statuses", e2eDocIDs(t, cs, map[string]any{"statuses": []string{"ACTIVE", "ARCHIVED"}}), "D-1", "D-2")

	res = e2eCall(t, cs, "doc_update", map[string]any{"id": tiedID, "fingerprint": arch["fingerprint"], "title": "Renamed"})
	if code := e2eErrorCode(t, res); code != "invalid" {
		t.Fatalf("edit of archived: error_code = %q (%s)", code, e2eText(res))
	}
	if !strings.Contains(e2eText(res), "restore") {
		t.Errorf("edit of archived does not mention restore: %s", e2eText(res))
	}

	restored := e2eOK(t, cs, "doc_restore", map[string]any{"id": tiedID, "fingerprint": arch["fingerprint"]})
	if restored["status"] != "ACTIVE" {
		t.Fatalf("doc_restore = %v", restored)
	}
	e2eWantIDs(t, "list after restore", e2eDocIDs(t, cs, nil), "D-1", "D-2")

	newer := e2eOK(t, cs, "doc_create", map[string]any{"kind": "decision", "title": "Pick again", "body": "We pick differently.", "brief_id": briefID})
	newerID := newer["id"].(string)

	res = e2eCall(t, cs, "doc_supersede", map[string]any{"id": tiedID, "fingerprint": restored["fingerprint"]})
	if !res.IsError {
		t.Errorf("supersede without a replacement succeeded: %s", e2eText(res))
	}

	archivedRepl := e2eOK(t, cs, "doc_create", map[string]any{"kind": "reference", "title": "Gone", "body": "x"})
	gone := e2eOK(t, cs, "doc_archive", map[string]any{"id": archivedRepl["id"], "fingerprint": archivedRepl["fingerprint"]})
	res = e2eCall(t, cs, "doc_supersede", map[string]any{"id": tiedID, "fingerprint": restored["fingerprint"], "superseded_by": gone["id"]})
	if code := e2eErrorCode(t, res); code != "invalid" {
		t.Errorf("supersede by archived: error_code = %q (%s)", code, e2eText(res))
	}
	if cur := e2eOK(t, cs, "doc_get", map[string]any{"id": tiedID}); cur["status"] != "ACTIVE" {
		t.Errorf("rejected supersede changed status to %v", cur["status"])
	}

	sup := e2eOK(t, cs, "doc_supersede", map[string]any{"id": tiedID, "fingerprint": restored["fingerprint"], "superseded_by": newerID})
	if sup["status"] != "SUPERSEDED" || sup["superseded_by"] != newerID {
		t.Fatalf("doc_supersede = %v", sup)
	}
	if cur := e2eOK(t, cs, "doc_get", map[string]any{"id": tiedID}); cur["superseded_by"] != newerID {
		t.Errorf("doc_get superseded_by = %v", cur["superseded_by"])
	}
	e2eWantIDs(t, "list after supersede", e2eDocIDs(t, cs, nil), "D-1", newerID)
	e2eWantIDs(t, "superseded list", e2eDocIDs(t, cs, map[string]any{"statuses": []string{"SUPERSEDED"}}), tiedID)
}

func TestE2EDocumentsRetentionAndPurge(t *testing.T) {
	keep := newE2EEnv(t)
	keep.init(t)
	keep.repo.WriteFile(".ralph/project.yaml", "documents:\n  versions:\n    keep: 2\n")
	cs := keep.connect(t, io.Discard)

	a := e2eOK(t, cs, "doc_create", map[string]any{"kind": "design", "title": "Limited", "body": "start"})
	e2eEditTimes(t, cs, a["id"].(string), 5)
	e2eWantVersions(t, cs, "D-1", 4, 5)
	if cur := e2eOK(t, cs, "doc_get", map[string]any{"id": "D-1"}); cur["body"] != "body 5" || cur["version"] != float64(6) {
		t.Errorf("current after edits = %v", cur)
	}

	e := newE2EEnv(t)
	e.init(t)
	cs2 := e.connect(t, io.Discard)
	b := e2eOK(t, cs2, "doc_create", map[string]any{"kind": "design", "title": "Unlimited", "body": "start"})
	id := b["id"].(string)
	lastFP := e2eEditTimes(t, cs2, id, 5)
	e2eWantVersions(t, cs2, id, 1, 2, 3, 4, 5)

	r := e.run(t, "docs", "purge", "--keep", "1")
	if r.code != 0 {
		t.Fatalf("dry run exit = %d, stderr %q", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, id+"  versions 1, 2, 3, 4") || !strings.Contains(r.stdout, "dry run: versions 4") || !strings.Contains(r.stdout, "nothing was removed") {
		t.Errorf("dry run output = %q", r.stdout)
	}
	e2eWantVersions(t, cs2, id, 1, 2, 3, 4, 5)

	r = e.run(t, "docs", "purge", "--keep", "1", "--yes")
	if r.code != 0 || !strings.Contains(r.stdout, "removed: versions 4") {
		t.Fatalf("purge exit = %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	e2eWantVersions(t, cs2, id, 5)
	res := e2eCall(t, cs2, "doc_get", map[string]any{"id": id, "version": 2})
	if code := e2eErrorCode(t, res); code != "not_found" {
		t.Errorf("removed version: error_code = %q (%s)", code, e2eText(res))
	}
	cur := e2eOK(t, cs2, "doc_get", map[string]any{"id": id})
	if cur["body"] != "body 5" || cur["version"] != float64(6) || cur["fingerprint"] != lastFP {
		t.Errorf("current document after purge = %v", cur)
	}

	r = e.run(t, "docs", "purge")
	if r.code != 1 || !strings.Contains(r.stderr, "say what to purge") {
		t.Errorf("purge alone: exit %d, stderr %q", r.code, r.stderr)
	}

	r = e.run(t, "docs", "purge", "--older-than", "90d", "--yes")
	if r.code != 0 || !strings.Contains(r.stdout, "nothing to purge") {
		t.Errorf("older-than: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	e2eWantVersions(t, cs2, id, 5)
}

func TestE2EDocumentsStatusCommandUnaffected(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)

	brief := e2eOK(t, cs, "unit_create", map[string]any{"level": "brief", "title": "Ship it", "body": "Please ship it."})
	e2eOK(t, cs, "unit_create", map[string]any{"level": "task", "parent_id": "", "title": "Loose task", "acceptance_criteria": "done"})

	before := e.run(t, "status")
	if before.code != 0 {
		t.Fatalf("ralph status: exit %d, %q", before.code, before.stderr)
	}
	beforeTools := statusOf(t, cs)

	e2eOK(t, cs, "doc_create", map[string]any{"kind": "architecture", "title": "Overview", "body": "The shape."})
	e2eOK(t, cs, "doc_create", map[string]any{"kind": "decision", "title": "Pick", "body": "We pick.", "brief_id": brief["id"]})

	after := e.run(t, "status")
	if after.code != 0 || after.stdout != before.stdout {
		t.Errorf("ralph status changed after documents:\nbefore %q\nafter  %q (exit %d)", before.stdout, after.stdout, after.code)
	}
	for _, text := range []string{"Overview", "Pick", "D-1", "D-2"} {
		if strings.Contains(after.stdout, text) {
			t.Errorf("ralph status mentions document text %q: %s", text, after.stdout)
		}
	}
	if st := statusOf(t, cs); fmt.Sprint(st) != fmt.Sprint(beforeTools) {
		t.Errorf("status tool changed after documents:\nbefore %v\nafter  %v", beforeTools, st)
	}

	listed := e2eOK(t, cs, "unit_list", nil)
	if units, _ := listed["units"].([]any); len(units) != 2 {
		t.Errorf("unit_list = %v, want only the brief and the task", listed)
	}
	for _, id := range []string{"D-1", "D-2"} {
		res := e2eCall(t, cs, "unit_get", map[string]any{"id": id})
		if !res.IsError {
			t.Errorf("unit_get %s succeeded: %s", id, e2eText(res))
		}
	}
	if got := e2eOK(t, cs, "unit_get", map[string]any{"id": "T-1"}); got["title"] != "Loose task" {
		t.Errorf("unit_get T-1 = %v", got)
	}
}
