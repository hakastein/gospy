package supervisor

import (
	"context"
	"fmt"
	"time"
)

// Restart policies, spelled as the --restart flag takes them.
const (
	RestartNo        = "no"
	RestartAlways    = "always"
	RestartOnError   = "onerror"
	RestartOnSuccess = "onsuccess"
)

// Values filled in for the zero fields of a RestartPolicy.
const (
	defaultBaseDelay              = time.Second
	defaultMaxDelay               = time.Minute
	defaultMaxConsecutiveFailures = 10
)

// A session this long profiled something before it died, so its failure opens a new streak: a
// nightly php-fpm reload must not spend the failure budget.
const healthySession = 30 * time.Second

// RestartPolicy decides whether a finished profiler session starts again, how long the
// supervisor pauses first, and how many consecutive failures it tolerates before it gives up.
// Every field except Mode has a default, so RestartPolicy{Mode: RestartAlways} is a usable value.
type RestartPolicy struct {
	// Mode is the rule itself: RestartNo, RestartAlways, RestartOnError or RestartOnSuccess.
	Mode string

	// BaseDelay is the pause before the first restart of a failure streak. It doubles with every
	// further failure. Zero means one second.
	BaseDelay time.Duration

	// MaxDelay caps the doubling. Zero means one minute; a value below BaseDelay is raised to it.
	MaxDelay time.Duration

	// MaxConsecutiveFailures is the failure budget: once a streak reaches it the supervisor stops
	// and reports the last session error. Zero means ten, a negative value never gives up.
	MaxConsecutiveFailures int

	// Now and After are the supervisor's clock. Nil means the real one.
	Now   func() time.Time
	After func(time.Duration) <-chan time.Time
}

// ValidateRestart reports whether the text names a policy the supervisor knows how to apply.
func ValidateRestart(restart string) error {
	switch restart {
	case RestartNo, RestartAlways, RestartOnError, RestartOnSuccess:
		return nil
	default:
		return fmt.Errorf("invalid restart option: %s", restart)
	}
}

func (policy RestartPolicy) withDefaults() RestartPolicy {
	if policy.BaseDelay <= 0 {
		policy.BaseDelay = defaultBaseDelay
	}
	if policy.MaxDelay <= 0 {
		policy.MaxDelay = defaultMaxDelay
	}
	policy.MaxDelay = max(policy.MaxDelay, policy.BaseDelay)
	if policy.MaxConsecutiveFailures == 0 {
		policy.MaxConsecutiveFailures = defaultMaxConsecutiveFailures
	}
	if policy.Now == nil {
		policy.Now = time.Now
	}
	if policy.After == nil {
		policy.After = time.After
	}

	return policy
}

// restartAllowed reports whether a session that ended with err starts again; anything the
// supervisor does not know, the unset value included, keeps the profiler stopped.
func (policy RestartPolicy) restartAllowed(err error) bool {
	switch policy.Mode {
	case RestartAlways:
		return true
	case RestartOnError:
		return err != nil
	case RestartOnSuccess:
		return err == nil
	default:
		return false
	}
}

func (policy RestartPolicy) exhausted(failures int) bool {
	return policy.MaxConsecutiveFailures > 0 && failures >= policy.MaxConsecutiveFailures
}

func (policy RestartPolicy) nextDelay(delay time.Duration) time.Duration {
	doubled := delay * 2
	if doubled > policy.MaxDelay || doubled < delay {
		return policy.MaxDelay
	}

	return doubled
}

// pause blocks for delay, reporting false when the context ended first.
func (policy RestartPolicy) pause(ctx context.Context, delay time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-policy.After(delay):
		return true
	}
}
