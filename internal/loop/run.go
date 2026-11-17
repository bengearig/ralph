package loop

import (
	"context"
	"fmt"
	"strconv"
)

// RunInput is a prepared slice to work through.
type RunInput struct {
	Plan Plan
}

// Summary is how a run ended.
type Summary struct {
	// Done counts the tasks this run finished.
	Done int
	// Blocked counts the tasks this run blocked by stalling.
	Blocked int
	// Stopped says why the loop ended before the slice was complete; empty when it was.
	Stopped string
	Branch  string
}

// Run works the plan's tasks in order. A stalled task or a task that is not
// ready ends the run with Summary.Stopped set; a hard stop returns its error.
func Run(ctx context.Context, d Deps, in RunInput) (Summary, error) {
	plan := in.Plan
	sum := Summary{Branch: plan.Branch}
	maxFailures, err := intSetting(d, "loop.max_failures")
	if err != nil {
		return sum, err
	}
	docBytes, err := intSetting(d, "loop.prompt_documents_bytes")
	if err != nil {
		return sum, err
	}
	for _, task := range plan.Runnable {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		res, err := RunTask(ctx, d, TaskInput{
			Task: task, Slice: plan.Slice, Branch: plan.Branch,
			MaxFailures: maxFailures, DocumentBytes: docBytes,
		})
		if err != nil {
			return sum, err
		}
		switch res.Outcome {
		case TaskDone:
			sum.Done++
		case TaskStalled:
			sum.Blocked++
			sum.Stopped = fmt.Sprintf("%s %s", task.ID, res.Reason)
			return sum, nil
		default:
			return sum, fmt.Errorf("task %s ended without a result", task.ID)
		}
	}
	if plan.Stop != nil {
		sum.Stopped = plan.Stop.Reason
	}
	return sum, nil
}

func intSetting(d Deps, path string) (int, error) {
	n, err := strconv.Atoi(settingValue(d, path))
	if err != nil {
		return 0, fmt.Errorf("setting %s is not a whole number: %w", path, err)
	}
	return n, nil
}
