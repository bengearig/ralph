package githubtest

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func do(t *testing.T, s *Server, method, path, body, token string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, s.URL()+"/repos/"+s.Owner()+"/"+s.Repo()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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

func create(t *testing.T, s *Server, head string) (*http.Response, []byte) {
	t.Helper()
	return do(t, s, http.MethodPost, "/pulls", `{"title":"T","head":"`+head+`","base":"main","body":"B","draft":true}`, s.Token())
}

func TestGitHubTestServerCreateConflict(t *testing.T) {
	s := New(t, Config{})
	resp, raw := create(t, s, "feature")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}
	var pr struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Draft  bool   `json:"draft"`
		Head   struct {
			Ref string `json:"ref"`
		} `json:"head"`
	}
	if err := json.Unmarshal(raw, &pr); err != nil {
		t.Fatal(err)
	}
	if pr.Number != 1 || pr.State != "open" || !pr.Draft || pr.Head.Ref != "feature" {
		t.Errorf("pull request = %+v", pr)
	}

	resp, raw = create(t, s, "feature")
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(raw), "already exists") {
		t.Errorf("second create = %d: %s", resp.StatusCode, raw)
	}

	s.Decline(1)
	if resp, raw = create(t, s, "feature"); resp.StatusCode != http.StatusCreated {
		t.Errorf("create after the first was closed = %d: %s", resp.StatusCode, raw)
	}
}

func TestGitHubTestServerGetAndList(t *testing.T) {
	s := New(t, Config{})
	create(t, s, "a")
	create(t, s, "b")
	s.Merge(1)

	if resp, _ := do(t, s, http.MethodGet, "/pulls/99", "", s.Token()); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown pull request = %d", resp.StatusCode)
	}
	_, raw := do(t, s, http.MethodGet, "/pulls/1", "", s.Token())
	if !strings.Contains(string(raw), `"merged_at":"`) || !strings.Contains(string(raw), `"state":"closed"`) {
		t.Errorf("merged pull request = %s", raw)
	}

	for query, want := range map[string]int{
		"?head=" + s.Owner() + ":a&state=all": 1,
		"?head=" + s.Owner() + ":b":           1,
		"?head=" + s.Owner() + ":a":           0,
		"?head=other:b&state=all":             0,
		"?state=closed":                       1,
		"?state=all":                          2,
	} {
		_, raw := do(t, s, http.MethodGet, "/pulls"+query, "", s.Token())
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err != nil || len(list) != want {
			t.Errorf("%s: %d items (%v), want %d", query, len(list), err, want)
		}
	}
}

func TestGitHubTestServerPaginates(t *testing.T) {
	s := New(t, Config{PageSize: 2})
	create(t, s, "feature")
	for i := 0; i < 5; i++ {
		s.AddComment(1, Comment{Author: "bob", Body: "c"})
	}

	var total int
	path := "/issues/1/comments?per_page=100"
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("too many pages")
		}
		resp, raw := do(t, s, http.MethodGet, path, "", s.Token())
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			t.Fatal(err)
		}
		total += len(items)
		link := resp.Header.Get("Link")
		if link == "" {
			if len(items) != 1 {
				t.Errorf("last page has %d items, want 1", len(items))
			}
			break
		}
		if len(items) != 2 {
			t.Errorf("page has %d items, want 2", len(items))
		}
		start := strings.Index(link, "<"+s.URL())
		end := strings.Index(link, ">")
		if start != 0 || !strings.Contains(link, `rel="next"`) || !strings.Contains(link, `rel="last"`) {
			t.Fatalf("Link = %q", link)
		}
		path = strings.TrimPrefix(link[1:end], s.URL()+"/repos/"+s.Owner()+"/"+s.Repo())
	}
	if total != 5 {
		t.Errorf("read %d comments, want 5", total)
	}
}

func TestGitHubTestServerFeedbackShapes(t *testing.T) {
	s := New(t, Config{})
	create(t, s, "feature")
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s.AddReview(1, Review{Author: "carol", State: "CHANGES_REQUESTED", Body: "fix", At: at})
	s.AddInline(1, Comment{Author: "alice", Body: "here", Path: "a.go", Line: 7})

	_, raw := do(t, s, http.MethodGet, "/pulls/1/reviews", "", s.Token())
	var reviews []struct {
		State       string `json:"state"`
		SubmittedAt string `json:"submitted_at"`
		User        struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := json.Unmarshal(raw, &reviews); err != nil || len(reviews) != 1 {
		t.Fatalf("reviews = %s (%v)", raw, err)
	}
	if reviews[0].State != "CHANGES_REQUESTED" || reviews[0].User.Login != "carol" || reviews[0].SubmittedAt != "2026-03-01T12:00:00Z" {
		t.Errorf("review = %+v", reviews[0])
	}

	_, raw = do(t, s, http.MethodGet, "/pulls/1/comments", "", s.Token())
	var inline []struct {
		Path string `json:"path"`
		Line int    `json:"line"`
	}
	if err := json.Unmarshal(raw, &inline); err != nil || len(inline) != 1 || inline[0].Path != "a.go" || inline[0].Line != 7 {
		t.Errorf("inline = %s (%v)", raw, err)
	}

	if resp, _ := do(t, s, http.MethodGet, "/pulls/9/reviews", "", s.Token()); resp.StatusCode != http.StatusNotFound {
		t.Errorf("reviews of an unknown pull request = %d", resp.StatusCode)
	}
}

func TestGitHubTestServerChecksCredential(t *testing.T) {
	s := New(t, Config{Token: "right"})
	for name, token := range map[string]string{"none": "", "wrong": "wrong"} {
		resp, raw := do(t, s, http.MethodGet, "/pulls", "", token)
		if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(raw), "Bad credentials") {
			t.Errorf("%s: %d: %s", name, resp.StatusCode, raw)
		}
	}
	if resp, _ := do(t, s, http.MethodGet, "/pulls", "", "right"); resp.StatusCode != http.StatusOK {
		t.Errorf("right token = %d", resp.StatusCode)
	}
	s.RejectCredentials()
	if resp, _ := do(t, s, http.MethodGet, "/pulls", "", "right"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("after RejectCredentials = %d", resp.StatusCode)
	}
}

func TestGitHubTestServerRateLimitAndDown(t *testing.T) {
	s := New(t, Config{})
	reset := time.Unix(1900000000, 0)
	s.RateLimit(reset)
	resp, _ := do(t, s, http.MethodGet, "/pulls", "", s.Token())
	if resp.StatusCode != http.StatusForbidden || resp.Header.Get("X-RateLimit-Remaining") != "0" || resp.Header.Get("X-RateLimit-Reset") != "1900000000" {
		t.Errorf("rate limited = %d, %v", resp.StatusCode, resp.Header)
	}

	s = New(t, Config{})
	s.BringDown()
	if resp, _ := do(t, s, http.MethodGet, "/pulls", "", s.Token()); resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("down = %d", resp.StatusCode)
	}
}

func TestGitHubTestServerTimesIncrease(t *testing.T) {
	s := New(t, Config{})
	create(t, s, "feature")
	s.AddComment(1, Comment{Author: "a", Body: "1"})
	s.AddComment(1, Comment{Author: "a", Body: "2"})
	_, raw := do(t, s, http.MethodGet, "/issues/1/comments", "", s.Token())
	var items []struct {
		CreatedAt time.Time `json:"created_at"`
	}
	if err := json.Unmarshal(raw, &items); err != nil || len(items) != 2 {
		t.Fatalf("%s (%v)", raw, err)
	}
	if items[1].CreatedAt.Sub(items[0].CreatedAt) < time.Second {
		t.Errorf("times %v and %v are less than a second apart", items[0].CreatedAt, items[1].CreatedAt)
	}
}
