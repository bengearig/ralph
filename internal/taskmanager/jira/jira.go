package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/bengearig/ralph/internal/adf"
	"github.com/bengearig/ralph/internal/taskmanager"
)

const (
	maxTitleChars = 255
	maxBodyChars  = 30000
	batchSize     = 50
	maxPages      = 20
	refLabelStart = "ralph-"
)

var issueFields = []string{"summary", "description", "status", "parent", "labels", "issuetype", "updated"}

type issue struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Fields struct {
		Summary     string          `json:"summary"`
		Description json.RawMessage `json:"description"`
		Status      struct {
			Name string `json:"name"`
		} `json:"status"`
		Parent *struct {
			Key string `json:"key"`
		} `json:"parent"`
		Labels    []string `json:"labels"`
		IssueType struct {
			Name string `json:"name"`
		} `json:"issuetype"`
	} `json:"fields"`
}

func (i issue) ticket() taskmanager.Ticket {
	t := taskmanager.Ticket{
		Key:    i.Key,
		ID:     i.ID,
		Type:   i.Fields.IssueType.Name,
		Title:  i.Fields.Summary,
		Body:   body(i.Key, i.Fields.Description),
		Status: i.Fields.Status.Name,
	}
	if i.Fields.Parent != nil {
		t.Parent = i.Fields.Parent.Key
	}
	for _, l := range i.Fields.Labels {
		if strings.HasPrefix(l, refLabelStart) {
			t.Ref = l
			break
		}
	}
	return t
}

func body(key string, raw json.RawMessage) string {
	if s := strings.TrimSpace(string(raw)); s == "" || s == "null" {
		return ""
	}
	doc, err := adf.ParseJSON(raw)
	if err != nil {
		slog.Warn("jira: the description of a ticket is not a valid document", "key", key, "error", err)
		return ""
	}
	return adf.ToMarkdown(doc)
}

type searchResult struct {
	Issues        []issue `json:"issues"`
	NextPageToken string  `json:"nextPageToken"`
	IsLast        bool    `json:"isLast"`
}

func (c *Client) projectPath() string {
	return "/rest/api/3/project/" + url.PathEscape(c.project)
}

func issuePath(key string) string { return "/rest/api/3/issue/" + url.PathEscape(key) }

type notFoundError struct{ msg string }

func (e *notFoundError) Error() string        { return e.msg }
func (e *notFoundError) Is(target error) bool { return target == taskmanager.ErrNotFound }

type projectInfo struct {
	IssueTypes []struct {
		Name string `json:"name"`
	} `json:"issueTypes"`
}

func (c *Client) readProject(ctx context.Context) (projectInfo, error) {
	var p projectInfo
	err := c.do(ctx, "GET", c.projectPath(), nil, nil, &p)
	if errors.Is(err, taskmanager.ErrNotFound) {
		return p, &notFoundError{fmt.Sprintf("project %s was not found or is not visible to this account", c.project)}
	}
	return p, err
}

// Check verifies the credentials and that the project is reachable.
func (c *Client) Check(ctx context.Context) (taskmanager.Account, error) {
	var me struct {
		DisplayName  string `json:"displayName"`
		EmailAddress string `json:"emailAddress"`
	}
	if err := c.do(ctx, "GET", "/rest/api/3/myself", nil, nil, &me); err != nil {
		return taskmanager.Account{}, err
	}
	if _, err := c.readProject(ctx); err != nil {
		return taskmanager.Account{}, err
	}
	return taskmanager.Account{Name: me.DisplayName, Email: me.EmailAddress}, nil
}

// Types lists the issue types of the project.
func (c *Client) Types(ctx context.Context) ([]string, error) {
	p, err := c.readProject(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(p.IssueTypes))
	for _, t := range p.IssueTypes {
		names = append(names, t.Name)
	}
	return names, nil
}

func quoteJQL(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func (c *Client) search(ctx context.Context, jql string, max int, pageToken string) (searchResult, error) {
	req := map[string]any{"jql": jql, "fields": issueFields}
	if max > 0 {
		req["maxResults"] = max
	}
	if pageToken != "" {
		req["nextPageToken"] = pageToken
	}
	var res searchResult
	err := c.do(ctx, "POST", "/rest/api/3/search/jql", nil, req, &res)
	return res, err
}

func (c *Client) read(ctx context.Context, key string) (issue, error) {
	var i issue
	err := c.do(ctx, "GET", issuePath(key), url.Values{"fields": {strings.Join(issueFields, ",")}}, nil, &i)
	return i, err
}

func keyNumber(key string) int {
	n, err := strconv.Atoi(key[strings.LastIndex(key, "-")+1:])
	if err != nil {
		return 0
	}
	return n
}

// Find returns the ticket carrying ref as a label; of a duplicate it returns
// the one created first.
func (c *Client) Find(ctx context.Context, ref string) (*taskmanager.Ticket, error) {
	res, err := c.search(ctx, "project = "+quoteJQL(c.project)+" AND labels = "+quoteJQL(ref), 2, "")
	if err != nil {
		return nil, err
	}
	if len(res.Issues) == 0 {
		return nil, nil
	}
	first := res.Issues[0]
	for _, i := range res.Issues[1:] {
		if keyNumber(i.Key) < keyNumber(first.Key) {
			first = i
		}
	}
	t := first.ticket()
	return &t, nil
}

func description(md string) any {
	md = adf.Limit(md, maxBodyChars)
	if strings.TrimSpace(md) == "" {
		return nil
	}
	return adf.FromMarkdown(md)
}

// Create makes a ticket and returns it as Jira holds it.
func (c *Client) Create(ctx context.Context, n taskmanager.New) (taskmanager.Ticket, error) {
	labels := []string{"ralph"}
	if n.Ref != "" {
		labels = append(labels, n.Ref)
	}
	fields := map[string]any{
		"project":   map[string]string{"key": c.project},
		"issuetype": map[string]string{"name": n.Type},
		"summary":   normalizeTitle(n.Title),
		"labels":    labels,
	}
	if d := description(n.Body); d != nil {
		fields["description"] = d
	}
	if n.Parent != "" {
		fields["parent"] = map[string]string{"key": n.Parent}
	}
	var created struct {
		Key string `json:"key"`
	}
	if err := c.do(ctx, "POST", "/rest/api/3/issue", nil, map[string]any{"fields": fields}, &created); err != nil {
		return taskmanager.Ticket{}, err
	}
	i, err := c.read(ctx, created.Key)
	if err != nil {
		return taskmanager.Ticket{}, fmt.Errorf("reading back ticket %s: %w", created.Key, err)
	}
	return i.ticket(), nil
}

// Update changes the title and the body of a ticket.
func (c *Client) Update(ctx context.Context, key string, f taskmanager.Fields) error {
	fields := map[string]any{
		"summary":     normalizeTitle(f.Title),
		"description": description(f.Body),
	}
	err := c.do(ctx, "PUT", issuePath(key), nil, map[string]any{"fields": fields}, nil)
	if errors.Is(err, taskmanager.ErrNotFound) {
		return fmt.Errorf("ticket %s: %w", key, err)
	}
	return err
}

// Get returns the tickets for keys. A ticket that was moved to another key is
// returned under the key asked for, with its Key set to the new one.
func (c *Client) Get(ctx context.Context, keys []string) (map[string]taskmanager.Ticket, error) {
	out := map[string]taskmanager.Ticket{}
	keys = dedupe(keys)
	for start := 0; start < len(keys); start += batchSize {
		batch := keys[start:min(start+batchSize, len(keys))]
		found, err := c.searchKeys(ctx, batch)
		if err != nil {
			return nil, err
		}
		for _, key := range batch {
			if t, ok := found[key]; ok {
				out[key] = t
				continue
			}
			t, ok, err := c.getOne(ctx, key)
			if err != nil {
				return nil, err
			}
			if ok {
				out[key] = t
			}
		}
	}
	return out, nil
}

func dedupe(keys []string) []string {
	var out []string
	for _, k := range keys {
		if k != "" && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// searchKeys looks a batch up by key. Jira can refuse a search that names a
// key it does not have; the keys are then asked for one by one.
func (c *Client) searchKeys(ctx context.Context, batch []string) (map[string]taskmanager.Ticket, error) {
	quoted := make([]string, len(batch))
	for i, k := range batch {
		quoted[i] = quoteJQL(k)
	}
	jql := "key in (" + strings.Join(quoted, ",") + ")"
	found := map[string]taskmanager.Ticket{}
	token := ""
	for range maxPages {
		res, err := c.search(ctx, jql, len(batch), token)
		if errors.Is(err, taskmanager.ErrInvalid) {
			return found, nil
		}
		if err != nil {
			return nil, err
		}
		for _, i := range res.Issues {
			found[i.Key] = i.ticket()
		}
		if res.IsLast || res.NextPageToken == "" {
			break
		}
		token = res.NextPageToken
	}
	return found, nil
}

func (c *Client) getOne(ctx context.Context, key string) (taskmanager.Ticket, bool, error) {
	i, err := c.read(ctx, key)
	if errors.Is(err, taskmanager.ErrNotFound) {
		return taskmanager.Ticket{}, false, nil
	}
	if err != nil {
		return taskmanager.Ticket{}, false, err
	}
	return i.ticket(), true, nil
}

// Move is not implemented yet.
func (c *Client) Move(context.Context, string, string, []string) (taskmanager.Move, error) {
	return taskmanager.Move{}, fmt.Errorf("%w: not implemented yet", taskmanager.ErrInvalid)
}

// Schedule is not implemented yet.
func (c *Client) Schedule(context.Context, string, string) error {
	return fmt.Errorf("%w: not implemented yet", taskmanager.ErrInvalid)
}

var lineBreaks = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")

func normalizeTitle(s string) string {
	s = strings.TrimSpace(lineBreaks.Replace(s))
	if r := []rune(s); len(r) > maxTitleChars {
		s = strings.TrimSpace(string(r[:maxTitleChars]))
	}
	return s
}

// Normalize returns the title and body as Jira holds them.
func (c *Client) Normalize(f taskmanager.Fields) taskmanager.Fields {
	return taskmanager.Fields{Title: normalizeTitle(f.Title), Body: adf.Normalize(adf.Limit(f.Body, maxBodyChars))}
}
