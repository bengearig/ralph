package jira

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/bengearig/ralph/internal/taskmanager"
)

const maxMoveSteps = 20

type invalidError struct{ msg string }

func (e *invalidError) Error() string        { return e.msg }
func (e *invalidError) Is(target error) bool { return target == taskmanager.ErrInvalid }

type transition struct {
	ID string `json:"id"`
	To struct {
		Name string `json:"name"`
	} `json:"to"`
}

func sameStatus(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func (c *Client) readStatus(ctx context.Context, key string) (string, error) {
	var i issue
	if err := c.do(ctx, "GET", issuePath(key), url.Values{"fields": {"status"}}, nil, &i); err != nil {
		return "", err
	}
	return i.Fields.Status.Name, nil
}

func (c *Client) transitions(ctx context.Context, key string) ([]transition, error) {
	var res struct {
		Transitions []transition `json:"transitions"`
	}
	if err := c.do(ctx, "GET", issuePath(key)+"/transitions", nil, nil, &res); err != nil {
		return nil, err
	}
	return res.Transitions, nil
}

func (c *Client) transit(ctx context.Context, key, id, target string) error {
	body := map[string]any{"transition": map[string]string{"id": id}}
	err := c.do(ctx, "POST", issuePath(key)+"/transitions", nil, body, nil)
	if errors.Is(err, taskmanager.ErrInvalid) {
		return &invalidError{"transition to " + target + " needs more than ralph can give: " +
			strings.TrimPrefix(err.Error(), taskmanager.ErrInvalid.Error()+": ")}
	}
	return err
}

func leadingTo(ts []transition, status string) *transition {
	for i := range ts {
		if sameStatus(ts[i].To.Name, status) {
			return &ts[i]
		}
	}
	return nil
}

func availableNames(ts []transition) string {
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		if !slices.Contains(names, t.To.Name) {
			names = append(names, t.To.Name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func flowIndex(flow []string, status string) int {
	return slices.IndexFunc(flow, func(s string) bool { return sameStatus(s, status) })
}

// Move puts a ticket in status by a direct transition or, failing that, by
// stepping through flow.
func (c *Client) Move(ctx context.Context, key, status string, flow []string) (taskmanager.Move, error) {
	current, err := c.readStatus(ctx, key)
	if err != nil {
		return taskmanager.Move{}, err
	}
	if sameStatus(current, status) {
		return taskmanager.Move{From: current, To: status}, nil
	}
	ts, err := c.transitions(ctx, key)
	if err != nil {
		return taskmanager.Move{}, err
	}
	move := taskmanager.Move{From: current}
	fail := func(format string, args ...any) (taskmanager.Move, error) {
		msg := fmt.Sprintf("cannot move %s from %s to %s: ", key, current, status) + fmt.Sprintf(format, args...)
		return move, &invalidError{msg}
	}
	if t := leadingTo(ts, status); t != nil {
		if err := c.transit(ctx, key, t.ID, status); err != nil {
			return move, err
		}
		return c.settle(ctx, key, move)
	}
	from, to := flowIndex(flow, current), flowIndex(flow, status)
	if from < 0 || to < 0 {
		return fail("no transition leads there; available: %s", availableNames(ts))
	}
	step := 1
	if to < from {
		step = -1
	}
	if (to-from)*step > maxMoveSteps {
		return fail("flow is too long")
	}
	at := current
	for i := from + step; ; i += step {
		next := flow[i]
		t := leadingTo(ts, next)
		if t == nil {
			move.To = at
			return fail("no transition from %s to %s; available: %s", at, next, availableNames(ts))
		}
		if err := c.transit(ctx, key, t.ID, next); err != nil {
			return move, err
		}
		at = t.To.Name
		if i == to {
			return c.settle(ctx, key, move)
		}
		move.Via = append(move.Via, at)
		if ts, err = c.transitions(ctx, key); err != nil {
			return move, err
		}
	}
}

// settle reads the status the ticket ended in.
func (c *Client) settle(ctx context.Context, key string, move taskmanager.Move) (taskmanager.Move, error) {
	end, err := c.readStatus(ctx, key)
	if err != nil {
		return move, err
	}
	move.To = end
	return move, nil
}
