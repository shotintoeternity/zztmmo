package zztgo

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// One status per generation error, on every route that maps them (M40.4).
// Wrapped, as the services return them.
func TestGenerationErrorStatusTable(t *testing.T) {
	wrap := func(err error) error { return fmt.Errorf("world %q: %w", "X", err) }
	cases := []struct {
		err  error
		want int
	}{
		{ErrGenerationRateLimited, http.StatusTooManyRequests},
		{ErrGenerationBudget, http.StatusTooManyRequests},
		{ErrGenerationUnavailable, http.StatusServiceUnavailable},
		{ErrGeneratedWorldOccupied, http.StatusConflict},
		{ErrGeneratedWorldNotYours, http.StatusConflict},
		{ErrGeneratedWorldIsCanonical, http.StatusConflict},
		{ErrGeneratedWorldModified, http.StatusConflict},
		{errors.New("world failed validation: bad grid"), http.StatusUnprocessableEntity},
	}
	for _, c := range cases {
		for _, err := range []error{c.err, wrap(c.err)} {
			if got := generationErrorStatus(err); got != c.want {
				t.Errorf("generationErrorStatus(%q) = %d, want %d", err, got, c.want)
			}
			if got := generateErrorStatus(err); got != c.want {
				t.Errorf("generateErrorStatus(%q) = %d, want %d", err, got, c.want)
			}
		}
	}

	// Only /api/generate reads the model API's rate limit out of its reply.
	upstream := errors.New(`Claude API returned 429 Too Many Requests: {"error":{"type":"rate_limit_error","message":"This request would exceed the rate limit"}}`)
	if got := generateErrorStatus(upstream); got != http.StatusTooManyRequests {
		t.Errorf("generate, upstream rate limit = %d, want 429", got)
	}
	if got := generationErrorStatus(upstream); got != http.StatusUnprocessableEntity {
		t.Errorf("museum/retry, upstream rate limit = %d, want 422", got)
	}
}
