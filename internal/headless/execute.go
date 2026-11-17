package headless

import (
	"context"

	"github.com/bengearig/ralph/internal/harness"
)

// Execute builds the invocation and parser from the adapter and runs them.
func Execute(ctx context.Context, a harness.Adapter, req harness.HeadlessRequest, opt Options) (Result, error) {
	if err := harness.CheckMode(a, req); err != nil {
		return Result{}, err
	}
	inv, err := a.Headless(req)
	if err != nil {
		return Result{}, err
	}
	return Run(ctx, inv, a.NewParser(), opt)
}
