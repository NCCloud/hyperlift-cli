package apps

import (
	"context"
	"errors"
	"time"

	"github.com/nccloud/hyperlift-cli/internal/client"
)

// errWaitTimeout marks that the wait's own timer expired. A poll can also fail
// with a deadline error of its own, e.g. an HTTP client timeout; that one is
// not this sentinel and surfaces unchanged.
var errWaitTimeout = errors.New("wait timed out")

// waitTarget selects the settled condition that waitForOutcome polls toward.
type waitTarget int

const (
	// waitBuild waits on build_status. See the fast-path edge below.
	waitBuild waitTarget = iota
	// waitStart waits for the application to reach `running`.
	waitStart
	// waitStop waits for the application to reach `stopped`.
	waitStop
	// waitRestart waits for the application to leave `running` and return to it.
	waitRestart
)

// defaultGrace is how long a wait accepts an already-settled state before the
// transient state appears — a stale `built` from the previous build, or the
// `running` from before the restart. This is the fast-path edge.
const defaultGrace = 10 * time.Second

// defaultInterval is the pause between polls.
const defaultInterval = 2 * time.Second

// waitOptions tunes how waitForOutcome polls.
type waitOptions struct {
	// Interval is the pause between polls.
	Interval time.Duration
	// Timeout bounds the whole wait. Zero means rely on ctx alone.
	Timeout time.Duration
	// Grace replaces the fast-path grace window; the default is 10s. Only
	// waitBuild and waitRestart use it.
	Grace time.Duration
}

// waitProgress records what the poll loop has seen.
type waitProgress struct {
	sawBuilding   bool // waitBuild: `building` was seen
	sawDisruption bool // waitRestart: a non-`running` status was seen
}

// waitForOutcome polls get until the operation settles for its target, the
// context is cancelled, or the timeout elapses. It returns the last Application
// it saw. get receives the timeout-bounded context, so the wait also cancels an
// in-flight poll.
func waitForOutcome(ctx context.Context, get func(context.Context) (*client.Application, error), target waitTarget, opts waitOptions) (*client.Application, error) {
	interval := opts.Interval
	if interval <= 0 {
		interval = defaultInterval
	}

	grace := opts.Grace
	if grace <= 0 {
		grace = defaultGrace
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		// The cause tells the wait's own timer apart from every other reason
		// the context, or a poll inside it, can fail.
		ctx, cancel = context.WithTimeoutCause(ctx, opts.Timeout, errWaitTimeout)
		defer cancel()
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	start := time.Now()

	var (
		progress waitProgress
		last     *client.Application
	)

	for {
		app, err := get(ctx)
		if err != nil {
			if errors.Is(context.Cause(ctx), errWaitTimeout) {
				return last, errWaitTimeout
			}

			return last, err
		}

		last = app
		if settled(app, target, &progress, time.Since(start) >= grace) {
			return app, nil
		}

		select {
		case <-ctx.Done():
			if errors.Is(context.Cause(ctx), errWaitTimeout) {
				return last, errWaitTimeout
			}

			return last, ctx.Err()
		case <-ticker.C:
		}
	}
}

// settled reports whether the application has reached the awaited outcome.
// graceElapsed matters only for the fast-path edges.
func settled(app *client.Application, target waitTarget, progress *waitProgress, graceElapsed bool) bool {
	switch target {
	case waitBuild:
		switch app.BuildStatus {
		case client.BuildStatusBuilding:
			progress.sawBuilding = true
			return false
		case client.BuildStatusBuilt, client.BuildStatusFailed:
			return progress.sawBuilding || graceElapsed
		default:
			// `none`, and any value added later, is not a build outcome. Never
			// report a build that has not started as finished.
			return false
		}
	case waitStop:
		return app.Status == client.StatusStopped || app.Status == client.StatusFailure
	case waitStart:
		return app.Status == client.StatusRunning || app.Status == client.StatusFailure
	case waitRestart:
		if app.Status == client.StatusFailure {
			return true
		}

		if app.Status != client.StatusRunning {
			progress.sawDisruption = true
			return false
		}
		// `running` is also the status before the restart, so it counts only
		// after an observed disruption, or after the grace window.
		return progress.sawDisruption || graceElapsed
	default:
		return false
	}
}
