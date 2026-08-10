package apps

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nccloud/hyperlift-cli/internal/client"
)

// scriptedGet returns the applications of a slice in order, and repeats the last
// entry after that. It counts the calls.
func scriptedGet(apps []*client.Application, calls *int) func(context.Context) (*client.Application, error) {
	return func(context.Context) (*client.Application, error) {
		i := *calls
		*calls++

		if i >= len(apps) {
			i = len(apps) - 1
		}

		return apps[i], nil
	}
}

func waitApp(status client.AppStatus, build client.BuildStatus) *client.Application {
	return &client.Application{ID: "app_x", Status: status, BuildStatus: build}
}

// realOpts are the production durations. The tests below that depend on time run
// inside a synctest bubble, where the clock is virtual: a ten-second grace window
// costs no real time, and elapsed time can be asserted exactly instead of with a
// margin.
func realOpts() waitOptions {
	return waitOptions{Interval: 2 * time.Second, Grace: 10 * time.Second, Timeout: 5 * time.Minute}
}

// wantElapsed fails unless the bubble's virtual clock advanced by exactly want.
func wantElapsed(t *testing.T, start time.Time, want time.Duration) {
	t.Helper()

	if got := time.Since(start); got != want {
		t.Errorf("elapsed = %v, want %v", got, want)
	}
}

func TestWaitForOutcome_StartReachesRunning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls int

		get := scriptedGet([]*client.Application{
			waitApp(client.StatusInStart, client.BuildStatusBuilt),
			waitApp(client.StatusRunning, client.BuildStatusBuilt),
		}, &calls)

		start := time.Now()

		got, err := waitForOutcome(context.Background(), get, waitStart, realOpts())
		if err != nil {
			t.Fatalf("waitForOutcome: %v", err)
		}

		if got.Status != client.StatusRunning {
			t.Errorf("status = %s, want running", got.Status)
		}

		if calls != 2 {
			t.Errorf("calls = %d, want 2 (transient observed, then running)", calls)
		}
		// One interval passed, so the wait polled again instead of settling on
		// the transient state.
		wantElapsed(t, start, 2*time.Second)
	})
}

func TestWaitForOutcome_StopReachesStopped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls int

		get := scriptedGet([]*client.Application{
			waitApp(client.StatusInStop, client.BuildStatusBuilt),
			waitApp(client.StatusStopped, client.BuildStatusBuilt),
		}, &calls)

		start := time.Now()

		got, err := waitForOutcome(context.Background(), get, waitStop, realOpts())
		if err != nil {
			t.Fatalf("waitForOutcome: %v", err)
		}

		if got.Status != client.StatusStopped {
			t.Errorf("status = %s, want stopped", got.Status)
		}

		if calls != 2 {
			t.Errorf("calls = %d, want 2 (transient observed, then stopped)", calls)
		}

		wantElapsed(t, start, 2*time.Second)
	})
}

func TestWaitForOutcome_StartFailureSettles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls int

		get := scriptedGet([]*client.Application{waitApp(client.StatusFailure, client.BuildStatusBuilt)}, &calls)

		start := time.Now()

		got, err := waitForOutcome(context.Background(), get, waitStart, realOpts())
		if err != nil {
			t.Fatalf("waitForOutcome: %v", err)
		}

		if got.Status != client.StatusFailure {
			t.Errorf("status = %s, want failure (settled so the command can report it)", got.Status)
		}

		if calls != 1 {
			t.Errorf("calls = %d, want 1 (failure settles at once)", calls)
		}
		// A failure is terminal, so the wait never reaches the first interval.
		wantElapsed(t, start, 0)
	})
}

func TestWaitForOutcome_BuildBuildingThenBuilt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls int

		get := scriptedGet([]*client.Application{
			waitApp(client.StatusDeploying, client.BuildStatusBuilding),
			waitApp(client.StatusDeploying, client.BuildStatusBuilt),
		}, &calls)

		start := time.Now()

		got, err := waitForOutcome(context.Background(), get, waitBuild, realOpts())
		if err != nil {
			t.Fatalf("waitForOutcome: %v", err)
		}

		if got.BuildStatus != client.BuildStatusBuilt {
			t.Errorf("build = %s, want built", got.BuildStatus)
		}

		if calls != 2 {
			t.Errorf("calls = %d, want 2 (building observed before built)", calls)
		}
		// `building` was seen, so the grace window never applied.
		wantElapsed(t, start, 2*time.Second)
	})
}

func TestWaitForOutcome_BuildFastTerminalEdge(t *testing.T) {
	// This build settles before the first poll: build_status is already terminal
	// and `building` never appears. The grace window is the only way out.
	synctest.Test(t, func(t *testing.T) {
		var calls int

		get := scriptedGet([]*client.Application{waitApp(client.StatusRunning, client.BuildStatusBuilt)}, &calls)

		start := time.Now()

		got, err := waitForOutcome(context.Background(), get, waitBuild, realOpts())
		if err != nil {
			t.Fatalf("waitForOutcome: %v", err)
		}

		if got.BuildStatus != client.BuildStatusBuilt {
			t.Errorf("build = %s, want built", got.BuildStatus)
		}
		// The wait accepted at the grace boundary, not before it.
		wantElapsed(t, start, 10*time.Second)
	})
}

func TestWaitForOutcome_BuildNoneNeverSettles(t *testing.T) {
	// `none` means the build never started, so it is not an outcome. The wait
	// must time out instead of reporting the build as complete.
	synctest.Test(t, func(t *testing.T) {
		var calls int

		get := scriptedGet([]*client.Application{waitApp(client.StatusRunning, client.BuildStatusNone)}, &calls)

		start := time.Now()

		_, err := waitForOutcome(context.Background(), get, waitBuild, realOpts())
		if !errors.Is(err, errWaitTimeout) {
			t.Fatalf("err = %v, want errWaitTimeout", err)
		}
		// It waited the whole timeout, so no grace shortcut applied.
		wantElapsed(t, start, 5*time.Minute)
	})
}

func TestWaitForOutcome_RestartIgnoresStaleRunning(t *testing.T) {
	// The first poll can still show the `running` from before the restart. To
	// settle on it would report success without seeing anything, so the wait must
	// hold until the restart disrupts the status.
	synctest.Test(t, func(t *testing.T) {
		var calls int

		get := scriptedGet([]*client.Application{
			waitApp(client.StatusRunning, client.BuildStatusBuilt),
			waitApp(client.StatusRestarting, client.BuildStatusBuilt),
			waitApp(client.StatusRunning, client.BuildStatusBuilt),
		}, &calls)

		opts := realOpts()
		opts.Grace = time.Hour // out of reach: only the observed sequence can settle this

		start := time.Now()

		got, err := waitForOutcome(context.Background(), get, waitRestart, opts)
		if err != nil {
			t.Fatalf("waitForOutcome: %v", err)
		}

		if got.Status != client.StatusRunning {
			t.Errorf("status = %s, want running", got.Status)
		}

		if calls != 3 {
			t.Errorf("calls = %d, want 3 (running -> restarting -> running observed)", calls)
		}

		wantElapsed(t, start, 4*time.Second)
	})
}

func TestWaitForOutcome_RestartFastEdgeWaitsForGrace(t *testing.T) {
	// This in-place restart is too quick to see: the status never leaves
	// `running`. The grace window is the only way out.
	synctest.Test(t, func(t *testing.T) {
		var calls int

		get := scriptedGet([]*client.Application{waitApp(client.StatusRunning, client.BuildStatusBuilt)}, &calls)

		start := time.Now()

		got, err := waitForOutcome(context.Background(), get, waitRestart, realOpts())
		if err != nil {
			t.Fatalf("waitForOutcome: %v", err)
		}

		if got.Status != client.StatusRunning {
			t.Errorf("status = %s, want running", got.Status)
		}

		wantElapsed(t, start, 10*time.Second)
	})
}

func TestWaitForOutcome_RestartFailureSettles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls int

		get := scriptedGet([]*client.Application{waitApp(client.StatusFailure, client.BuildStatusBuilt)}, &calls)

		start := time.Now()

		got, err := waitForOutcome(context.Background(), get, waitRestart, realOpts())
		if err != nil {
			t.Fatalf("waitForOutcome: %v", err)
		}

		if got.Status != client.StatusFailure {
			t.Errorf("status = %s, want failure (settled so the command can report it)", got.Status)
		}
		// A failure settles even though no disruption was observed.
		wantElapsed(t, start, 0)
	})
}

func TestWaitForOutcome_TimeoutCancelsInFlightPoll(t *testing.T) {
	// The timeout must reach the poll itself, not only the gap between polls.
	synctest.Test(t, func(t *testing.T) {
		get := func(ctx context.Context) (*client.Application, error) {
			<-ctx.Done()
			// A cancelled poll reports what it has, and the wait discards it.
			return waitApp(client.StatusInStart, client.BuildStatusBuilt), ctx.Err()
		}

		start := time.Now()

		_, err := waitForOutcome(context.Background(), get, waitStart, realOpts())
		if !errors.Is(err, errWaitTimeout) {
			t.Fatalf("err = %v, want errWaitTimeout from the cancelled poll", err)
		}

		wantElapsed(t, start, 5*time.Minute)
	})
}

func TestWaitForOutcome_Timeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls int

		get := scriptedGet([]*client.Application{waitApp(client.StatusInStart, client.BuildStatusBuilt)}, &calls) // never reaches running

		start := time.Now()

		_, err := waitForOutcome(context.Background(), get, waitStart, realOpts())

		// The wait's own timer must report the sentinel, so the command can tell
		// this timeout apart from a poll's own deadline error.
		if !errors.Is(err, errWaitTimeout) {
			t.Fatalf("err = %v, want errWaitTimeout", err)
		}

		wantElapsed(t, start, 5*time.Minute)
	})
}

func TestWaitForOutcome_PollDeadlineErrorIsNotTimeout(t *testing.T) {
	// An HTTP client timeout inside a poll also wraps context.DeadlineExceeded.
	// It must surface as the poll's own error, never as the wait timeout.
	synctest.Test(t, func(t *testing.T) {
		reqErr := fmt.Errorf("Get \"/apps/app_x\": %w", context.DeadlineExceeded)
		get := func(context.Context) (*client.Application, error) { return nil, reqErr }

		start := time.Now()

		_, err := waitForOutcome(context.Background(), get, waitStart, realOpts())

		if errors.Is(err, errWaitTimeout) {
			t.Fatalf("err = %v, must not be the wait timeout", err)
		}

		if !errors.Is(err, reqErr) {
			t.Fatalf("err = %v, want the poll's request error", err)
		}
		// The poll's own error returns at once, without waiting out the timeout.
		wantElapsed(t, start, 0)
	})
}

func TestWaitForOutcome_ContextCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls int

		get := scriptedGet([]*client.Application{waitApp(client.StatusInStart, client.BuildStatusBuilt)}, &calls)

		ctx, cancel := context.WithCancel(context.Background())

		go func() {
			time.Sleep(5 * time.Second)
			cancel()
		}()

		start := time.Now()

		_, err := waitForOutcome(ctx, get, waitStart, realOpts())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		// The caller's cancel wins over the wait's own timer.
		if errors.Is(err, errWaitTimeout) {
			t.Fatal("cancel reported as the wait timeout")
		}
		// The wait returned the moment cancel landed, mid-interval.
		wantElapsed(t, start, 5*time.Second)
	})
}

// TestWaitForOutcome_ZeroOptionsUseDefaults pins defaultInterval and
// defaultGrace. Every other test passes explicit durations, so without this a
// change to either constant would break real waits with no test failing.
func TestWaitForOutcome_ZeroOptionsUseDefaults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls int

		get := scriptedGet([]*client.Application{
			waitApp(client.StatusInStart, client.BuildStatusBuilt),
			waitApp(client.StatusRunning, client.BuildStatusBuilt),
		}, &calls)

		start := time.Now()

		if _, err := waitForOutcome(context.Background(), get, waitStart, waitOptions{}); err != nil {
			t.Fatalf("waitForOutcome: %v", err)
		}
		// One default interval passed between the two polls.
		wantElapsed(t, start, defaultInterval)
	})

	synctest.Test(t, func(t *testing.T) {
		var calls int

		// Already built, and `building` never appears, so only the grace window
		// can settle this one.
		get := scriptedGet([]*client.Application{waitApp(client.StatusRunning, client.BuildStatusBuilt)}, &calls)

		start := time.Now()

		if _, err := waitForOutcome(context.Background(), get, waitBuild, waitOptions{}); err != nil {
			t.Fatalf("waitForOutcome: %v", err)
		}

		wantElapsed(t, start, defaultGrace)
	})
}
