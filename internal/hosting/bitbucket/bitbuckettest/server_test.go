package bitbuckettest

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func request(t *testing.T, s *Server, method, path, body, auth string) (*http.Response, []byte) {
	t.Helper()
	target := s.URL() + "/repositories/" + s.Workspace() + "/" + s.Repo() + path
	if strings.HasPrefix(path, "http") {
		target = path
	}
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, raw
}

func bearer(s *Server) string { return "Bearer " + s.Token() }

func create(t *testing.T, s *Server, head string) (*http.Response, []byte) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"title": "T", "description": "D", "draft": true,
		"source": map[string]any{"branch": map[string]any{"name": head}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return request(t, s, http.MethodPost, "/pullrequests", string(body), bearer(s))
}

type page struct {
	Size   int               `json:"size"`
	Next   string            `json:"next"`
	Values []json.RawMessage `json:"values"`
}

func list(t *testing.T, s *Server, query string) page {
	t.Helper()
	resp, raw := request(t, s, http.MethodGet, "/pullrequests?"+query, "", bearer(s))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list %q = %d: %s", query, resp.StatusCode, raw)
	}
	var pg page
	if err := json.Unmarshal(raw, &pg); err != nil {
		t.Fatal(err)
	}
	return pg
}

func ids(t *testing.T, pg page) []int {
	t.Helper()
	var out []int
	for _, v := range pg.Values {
		var pr struct {
			ID int `json:"id"`
		}
		if err := json.Unmarshal(v, &pr); err != nil {
			t.Fatal(err)
		}
		out = append(out, pr.ID)
	}
	return out
}

func TestBitbucketTestServerCreateConflict(t *testing.T) {
	s := New(t, Config{})
	resp, raw := create(t, s, "feature")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}
	var pr struct {
		ID          int    `json:"id"`
		State       string `json:"state"`
		Draft       bool   `json:"draft"`
		Destination struct {
			Branch struct {
				Name string `json:"name"`
			} `json:"branch"`
		} `json:"destination"`
	}
	if err := json.Unmarshal(raw, &pr); err != nil {
		t.Fatal(err)
	}
	if pr.ID != 1 || pr.State != "OPEN" || !pr.Draft || pr.Destination.Branch.Name != "main" {
		t.Errorf("pull request = %+v", pr)
	}

	resp, raw = create(t, s, "feature")
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), "pull request already exists") {
		t.Errorf("second create = %d: %s", resp.StatusCode, raw)
	}

	s.Decline(1)
	if resp, raw = create(t, s, "feature"); resp.StatusCode != http.StatusCreated {
		t.Errorf("create after the first was declined = %d: %s", resp.StatusCode, raw)
	}

	resp, raw = request(t, s, http.MethodPost, "/pullrequests", `{"description":"x"}`, bearer(s))
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), `"fields"`) || !strings.Contains(string(raw), "title") {
		t.Errorf("create without fields = %d: %s", resp.StatusCode, raw)
	}
}

func TestBitbucketTestServerGet(t *testing.T) {
	s := New(t, Config{})
	create(t, s, "feature")
	s.Merge(1)
	s.AddReview(1, Review{Author: "carol", State: "approved", At: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)})

	if resp, raw := request(t, s, http.MethodGet, "/pullrequests/42", "", bearer(s)); resp.StatusCode != http.StatusNotFound || !strings.Contains(string(raw), `"type":"error"`) {
		t.Errorf("unknown pull request = %d: %s", resp.StatusCode, raw)
	}
	_, raw := request(t, s, http.MethodGet, "/pullrequests/1", "", bearer(s))
	var pr struct {
		State       string `json:"state"`
		MergeCommit struct {
			Hash string `json:"hash"`
		} `json:"merge_commit"`
		Participants []struct {
			State          string `json:"state"`
			Approved       bool   `json:"approved"`
			ParticipatedOn string `json:"participated_on"`
			User           struct {
				DisplayName string `json:"display_name"`
			} `json:"user"`
		} `json:"participants"`
	}
	if err := json.Unmarshal(raw, &pr); err != nil {
		t.Fatal(err)
	}
	if pr.State != "MERGED" || pr.MergeCommit.Hash == "" || len(pr.Participants) != 1 {
		t.Fatalf("pull request = %s", raw)
	}
	p := pr.Participants[0]
	if p.State != "approved" || !p.Approved || p.User.DisplayName != "carol" || !strings.HasPrefix(p.ParticipatedOn, "2026-03-01T12:00:00") {
		t.Errorf("participant = %+v", p)
	}

	s.AddReview(1, Review{Author: "carol", State: "changes_requested"})
	_, raw = request(t, s, http.MethodGet, "/pullrequests/1", "", bearer(s))
	if strings.Count(string(raw), `"type":"participant"`) != 1 || !strings.Contains(string(raw), `"state":"changes_requested"`) {
		t.Errorf("a reviewer's later verdict must replace the earlier one: %s", raw)
	}
}

func TestBitbucketTestServerFilterAndSort(t *testing.T) {
	s := New(t, Config{})
	create(t, s, "a")
	create(t, s, "b")
	s.Decline(1)
	create(t, s, "a")
	s.Merge(2)
	create(t, s, `we"ird\name`)

	cases := []struct {
		name  string
		query url.Values
		want  []int
	}{
		{"open by default", url.Values{}, []int{3, 4}},
		{"filter open", url.Values{"q": {`source.branch.name="a"`}, "state": {"OPEN"}}, []int{3}},
		{"declined", url.Values{"q": {`source.branch.name="a"`}, "state": {"DECLINED"}}, []int{1}},
		{"all states oldest first", url.Values{"q": {`source.branch.name="a"`}, "state": {"OPEN", "MERGED", "DECLINED", "SUPERSEDED"}}, []int{1, 3}},
		{"all states newest first", url.Values{"q": {`source.branch.name="a"`}, "state": {"OPEN", "MERGED", "DECLINED", "SUPERSEDED"}, "sort": {"-created_on"}}, []int{3, 1}},
		{"merged", url.Values{"state": {"MERGED"}}, []int{2}},
		{"escaped quote and backslash", url.Values{"q": {`source.branch.name="we\"ird\\name"`}}, []int{4}},
		{"no match", url.Values{"q": {`source.branch.name="nope"`}, "state": {"OPEN", "MERGED"}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ids(t, list(t, s, c.query.Encode()))
			if len(got) != len(c.want) {
				t.Fatalf("ids = %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("ids = %v, want %v", got, c.want)
				}
			}
		})
	}

	resp, _ := request(t, s, http.MethodGet, "/pullrequests?q="+url.QueryEscape(`title~"x"`), "", bearer(s))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unsupported filter = %d, want 400", resp.StatusCode)
	}
}

func TestBitbucketTestServerPaginates(t *testing.T) {
	s := New(t, Config{PageSize: 2})
	create(t, s, "feature")
	for i := 0; i < 5; i++ {
		s.AddComment(1, Comment{Author: "bob", Body: "c"})
	}
	s.AddInline(1, Comment{Author: "bob", Body: "i", Path: "a.go", Line: 3})

	var total, pages int
	target := "/pullrequests/1/comments?pagelen=100"
	for target != "" {
		if pages++; pages > 5 {
			t.Fatal("too many pages")
		}
		resp, raw := request(t, s, http.MethodGet, target, "", bearer(s))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%d: %s", resp.StatusCode, raw)
		}
		var pg page
		if err := json.Unmarshal(raw, &pg); err != nil {
			t.Fatal(err)
		}
		if pg.Size != 6 || len(pg.Values) > 2 {
			t.Errorf("size = %d, page of %d values", pg.Size, len(pg.Values))
		}
		total += len(pg.Values)
		target = pg.Next
		if target != "" && !strings.HasPrefix(target, s.URL()) {
			t.Fatalf("next = %q is not a full URL on the server", target)
		}
	}
	if total != 6 || pages != 3 {
		t.Errorf("read %d comments in %d pages, want 6 in 3", total, pages)
	}
}

func TestBitbucketTestServerCommentShapes(t *testing.T) {
	s := New(t, Config{})
	create(t, s, "feature")
	s.AddComment(1, Comment{Author: "bob", Body: "general"})
	s.AddInline(1, Comment{Author: "alice", Body: "inline", Path: "pkg/a.go", Line: 12})

	_, raw := request(t, s, http.MethodGet, "/pullrequests/1/comments", "", bearer(s))
	var pg struct {
		Values []struct {
			Content struct {
				Raw string `json:"raw"`
			} `json:"content"`
			User struct {
				DisplayName string `json:"display_name"`
			} `json:"user"`
			Inline *struct {
				Path string `json:"path"`
				From *int   `json:"from"`
				To   *int   `json:"to"`
			} `json:"inline"`
			Deleted bool `json:"deleted"`
			Pending bool `json:"pending"`
		} `json:"values"`
	}
	if err := json.Unmarshal(raw, &pg); err != nil || len(pg.Values) != 2 {
		t.Fatalf("%s (%v)", raw, err)
	}
	if v := pg.Values[0]; v.Inline != nil || v.Content.Raw != "general" || v.User.DisplayName != "bob" || v.Deleted || v.Pending {
		t.Errorf("general comment = %+v", v)
	}
	if v := pg.Values[1]; v.Inline == nil || v.Inline.Path != "pkg/a.go" || v.Inline.To == nil || *v.Inline.To != 12 || v.Inline.From != nil {
		t.Errorf("inline comment = %+v", v)
	}
	if resp, _ := request(t, s, http.MethodGet, "/pullrequests/9/comments", "", bearer(s)); resp.StatusCode != http.StatusNotFound {
		t.Errorf("comments of an unknown pull request = %d", resp.StatusCode)
	}
}

func TestBitbucketTestServerChecksCredential(t *testing.T) {
	bearerServer := New(t, Config{Token: "right"})
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("person@example.com:right"))
	basicServer := New(t, Config{Token: "right", User: "person@example.com"})

	cases := []struct {
		name string
		srv  *Server
		auth string
		want int
	}{
		{"bearer none", bearerServer, "", http.StatusUnauthorized},
		{"bearer wrong", bearerServer, "Bearer wrong", http.StatusUnauthorized},
		{"bearer right", bearerServer, "Bearer right", http.StatusOK},
		{"bearer server refuses basic", bearerServer, basic, http.StatusUnauthorized},
		{"basic server refuses bearer", basicServer, "Bearer right", http.StatusUnauthorized},
		{"basic wrong user", basicServer, "Basic " + base64.StdEncoding.EncodeToString([]byte("other@example.com:right")), http.StatusUnauthorized},
		{"basic right", basicServer, basic, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, raw := request(t, c.srv, http.MethodGet, "/pullrequests", "", c.auth)
			if resp.StatusCode != c.want {
				t.Errorf("status = %d, want %d: %s", resp.StatusCode, c.want, raw)
			}
		})
	}

	bearerServer.RejectCredentials()
	if resp, _ := request(t, bearerServer, http.MethodGet, "/pullrequests", "", "Bearer right"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("after RejectCredentials = %d", resp.StatusCode)
	}
}

func TestBitbucketTestServerRateLimitAndDown(t *testing.T) {
	s := New(t, Config{})
	s.RateLimit(time.Now().Add(90 * time.Second))
	resp, _ := request(t, s, http.MethodGet, "/pullrequests", "", bearer(s))
	secs := resp.Header.Get("Retry-After")
	if resp.StatusCode != http.StatusTooManyRequests || (secs != "90" && secs != "89" && secs != "91") {
		t.Errorf("rate limited = %d, Retry-After %q", resp.StatusCode, secs)
	}

	s = New(t, Config{})
	s.BringDown()
	if resp, _ := request(t, s, http.MethodGet, "/pullrequests", "", bearer(s)); resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("down = %d", resp.StatusCode)
	}
}
