package executor

import (
	"context"
	"errors"
)

// Metadata keys set by the egress-guard plugin. They are not a global
// request-retry budget: zero disables an in-request credential switch.
const (
	MetadataThinkingRetry           = "egress_thinking_retry"
	MetadataThinkingMinOutputTokens = "egress_thinking_min_output_tokens"
)

// ThinkingFailuresRelease forces the current attempt to stream through even
// when reasoning text is missing. The conductor uses it for the last allowed
// attempt and for the replay that runs when no other credential is left.
const ThinkingFailuresRelease = 1 << 20

type thinkingFailuresKey struct{}

// WithThinkingFailures records how many credentials in this request already
// failed the thinking gate. The count survives upstream-attempt context wrappers.
func WithThinkingFailures(ctx context.Context, n int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if n < 0 {
		n = 0
	}
	return context.WithValue(ctx, thinkingFailuresKey{}, n)
}

// ThinkingFailures returns the number of thinking-gate failures already spent.
func ThinkingFailures(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	n, _ := ctx.Value(thinkingFailuresKey{}).(int)
	if n < 0 {
		return 0
	}
	return n
}

// reasoningAbsentError is a pre-commit stream failure. It is retryable inside
// the current credential round and is not credential-scoped, so the conductor
// keeps picking the next auth. It has no HTTP status, so it does not start
// another request-retry round after the round's own budget is spent.
type reasoningAbsentError struct{}

func (reasoningAbsentError) Error() string {
	return "reasoning_absent: visible output has no reasoning text"
}

// NewReasoningAbsentError reports that client-visible prose started without
// reasoning text and the attempt may be replaced.
func NewReasoningAbsentError() error {
	return reasoningAbsentError{}
}

// IsReasoningAbsent reports whether err is a thinking-gate failure.
func IsReasoningAbsent(err error) bool {
	var target reasoningAbsentError
	return errors.As(err, &target)
}
