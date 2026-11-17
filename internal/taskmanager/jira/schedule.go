package jira

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/bengearig/ralph/internal/taskmanager"
)

const sprintPageSize = 50

type agilePage[T any] struct {
	Values     []T  `json:"values"`
	StartAt    int  `json:"startAt"`
	IsLast     bool `json:"isLast"`
	MaxResults int  `json:"maxResults"`
}

type board struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type sprint struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// pages follows an agile listing until Jira says it is last.
func pages[T any](ctx context.Context, c *Client, path string, query url.Values) ([]T, error) {
	var all []T
	for start, n := 0, 0; n < maxPages; n++ {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		q.Set("startAt", strconv.Itoa(start))
		q.Set("maxResults", strconv.Itoa(sprintPageSize))
		var page agilePage[T]
		if err := c.do(ctx, "GET", path, q, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Values...)
		if page.IsLast || len(page.Values) == 0 {
			break
		}
		start += len(page.Values)
	}
	return all, nil
}

func (c *Client) resolveBoard(ctx context.Context) (int, error) {
	if c.board != 0 {
		return c.board, nil
	}
	c.mu.Lock()
	id := c.foundBoard
	c.mu.Unlock()
	if id != 0 {
		return id, nil
	}
	boards, err := pages[board](ctx, c, "/rest/agile/1.0/board", url.Values{"projectKeyOrId": {c.project}, "type": {"scrum"}})
	if err != nil {
		return 0, err
	}
	switch len(boards) {
	case 0:
		return 0, &invalidError{fmt.Sprintf("project %s has no scrum board, so there are no sprints; unset jira.sprint", c.project)}
	case 1:
		c.mu.Lock()
		c.foundBoard = boards[0].ID
		c.mu.Unlock()
		return boards[0].ID, nil
	}
	sort.Slice(boards, func(i, j int) bool { return boards[i].ID < boards[j].ID })
	parts := make([]string, len(boards))
	for i, b := range boards {
		parts[i] = fmt.Sprintf("%d: %s", b.ID, b.Name)
	}
	return 0, &invalidError{fmt.Sprintf("project %s has several scrum boards (%s); set jira.board", c.project, strings.Join(parts, ", "))}
}

func (c *Client) resolveSprint(ctx context.Context, plan string) (int, error) {
	c.mu.Lock()
	id, ok := c.sprints[plan]
	c.mu.Unlock()
	if ok {
		return id, nil
	}
	boardID, err := c.resolveBoard(ctx)
	if err != nil {
		return 0, err
	}
	list, err := pages[sprint](ctx, c, fmt.Sprintf("/rest/agile/1.0/board/%d/sprint", boardID), url.Values{"state": {"active,future"}})
	if err != nil {
		return 0, err
	}
	found := sprint{}
	for _, s := range list {
		if s.Name == plan && (found.ID == 0 || s.State == "active" && found.State != "active") {
			found = s
		}
	}
	if found.ID == 0 {
		return 0, &notFoundError{fmt.Sprintf("sprint %q was not found among the active and future sprints of board %d", plan, boardID)}
	}
	c.mu.Lock()
	c.sprints[plan] = found.ID
	c.mu.Unlock()
	return found.ID, nil
}

// Schedule puts a ticket in the active or future sprint named plan.
func (c *Client) Schedule(ctx context.Context, key, plan string) error {
	id, err := c.resolveSprint(ctx, plan)
	if err != nil {
		return err
	}
	body := map[string]any{"issues": []string{key}}
	err = c.do(ctx, "POST", fmt.Sprintf("/rest/agile/1.0/sprint/%d/issue", id), nil, body, nil)
	if errors.Is(err, taskmanager.ErrInvalid) || errors.Is(err, taskmanager.ErrNotFound) {
		c.mu.Lock()
		delete(c.sprints, plan)
		c.mu.Unlock()
	}
	return err
}
