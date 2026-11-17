package tickets

import (
	"strings"

	"github.com/bengearig/ralph/internal/taskmanager"
)

// Drift is how a unit's ticket differs from what ralph last pushed. Fields name the differences in the order
// title, body, status, or hold deleted alone. MovedTo is the new key of a ticket the system moved.
type Drift struct {
	Unit, Key string
	Fields    []string
	MovedTo   string
}

// Plan works out the drifts of observations. It is pure; normalize says what the system would hold for a value.
func Plan(obs []Observation, normalize func(taskmanager.Fields) taskmanager.Fields) []Drift {
	var out []Drift
	for _, o := range obs {
		dr := Drift{Unit: o.Unit, Key: o.Row.Key}
		if o.Ticket == nil {
			dr.Fields = []string{driftDeleted}
			out = append(out, dr)
			continue
		}
		norm := normalize(taskmanager.Fields{Title: o.Ticket.Title, Body: o.Ticket.Body})
		if o.Row.PushedTitle != "" && Hash(norm.Title) != o.Row.PushedTitle {
			dr.Fields = append(dr.Fields, "title")
		}
		if o.Row.PushedBody != "" && Hash(norm.Body) != o.Row.PushedBody {
			dr.Fields = append(dr.Fields, "body")
		}
		if o.Row.PushedStatus != "" && !strings.EqualFold(o.Ticket.Status, o.Row.PushedStatus) {
			dr.Fields = append(dr.Fields, "status")
		}
		if o.Ticket.Key != "" && o.Ticket.Key != o.Row.Key {
			dr.MovedTo = o.Ticket.Key
		}
		if len(dr.Fields) > 0 || dr.MovedTo != "" {
			out = append(out, dr)
		}
	}
	return out
}
