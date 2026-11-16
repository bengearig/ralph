// Package workflow holds the harness-neutral workflow instruction texts.
package workflow

// Instruction is one workflow instruction an adapter presents to its harness.
type Instruction struct {
	Name         string
	Description  string
	ArgumentHint string
	Body         string
}
