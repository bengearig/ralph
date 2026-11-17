// Package followup observes pull requests and plans what to do about them.
// Observing and planning write nothing; Record writes only the state change
// that prstep.Apply writes.
package followup

// Kind says what a follow-up is.
type Kind string

const (
	KindRecord   Kind = "record"
	KindDecision Kind = "decision"
	KindFeedback Kind = "feedback"
	KindClose    Kind = "close"
	KindContinue Kind = "continue"
)

// Order ranks the kinds in the order a slice's follow-ups are handled.
func (k Kind) Order() int {
	switch k {
	case KindRecord:
		return 0
	case KindDecision:
		return 1
	case KindFeedback:
		return 2
	case KindClose:
		return 3
	case KindContinue:
		return 4
	}
	return 5
}

// Followup is one thing to do about a slice.
type Followup struct {
	Kind    Kind
	Slice   string
	Summary string
	// Number is the pull request the follow-up is about.
	Number int
}

// Branch deletion modes.
const (
	DeleteLocal = "local"
	DeleteBoth  = "both"
	DeleteNone  = "none"
)
