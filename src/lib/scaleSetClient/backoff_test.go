package scaleSetClient

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// retryablehttp's DefaultBackoff returns a Retry-After header verbatim for 429
// and 503, bypassing the RetryWaitMax clamp. GitHub sends that header on rate
// limits, so without a bound one GetMessage could block far past
// MaxPollDuration and a healthy listener would read as dead.
func TestBoundedBackoff_ClampsRetryAfter(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"600"}},
	}

	wait := boundedBackoff(pollRetryWaitMin, pollRetryWaitMax, 0, resp)

	assert.LessOrEqual(t, wait, pollRetryWaitMax,
		"a 10-minute Retry-After must not escape the budget")
}

func TestBoundedBackoff_ClampsServiceUnavailable(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{"Retry-After": []string{"300"}},
	}

	wait := boundedBackoff(pollRetryWaitMin, pollRetryWaitMax, 0, resp)

	assert.LessOrEqual(t, wait, pollRetryWaitMax)
}

// Every attempt must stay inside the ceiling MaxPollDuration is built from.
func TestBoundedBackoff_NeverExceedsMaxForAnyAttempt(t *testing.T) {
	cases := []*http.Response{
		nil,
		{StatusCode: http.StatusOK},
		{StatusCode: http.StatusTooManyRequests,
			Header: http.Header{"Retry-After": []string{"9999"}}},
		{StatusCode: http.StatusServiceUnavailable,
			Header: http.Header{"Retry-After": []string{"Wed, 21 Oct 2099 07:28:00 GMT"}}},
	}

	for _, resp := range cases {
		for attempt := range pollRetryMax + 1 {
			wait := boundedBackoff(pollRetryWaitMin, pollRetryWaitMax, attempt, resp)
			assert.LessOrEqual(t, wait, pollRetryWaitMax)
			assert.GreaterOrEqual(t, wait, time.Duration(0))
		}
	}
}

// Ordinary backoff still grows, so the clamp did not flatten it.
func TestBoundedBackoff_KeepsExponentialGrowth(t *testing.T) {
	first := boundedBackoff(pollRetryWaitMin, pollRetryWaitMax, 0, nil)
	second := boundedBackoff(pollRetryWaitMin, pollRetryWaitMax, 1, nil)

	assert.Equal(t, pollRetryWaitMin, first)
	assert.Greater(t, second, first)
}
