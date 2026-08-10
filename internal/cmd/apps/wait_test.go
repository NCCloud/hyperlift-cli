package apps

import (
	"context"
	"errors"
	"fmt"
	"testing"
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

func fastOpts() waitOptions {
	return waitOptions{Interval: time.Millisecond, Grace: 20 * time.Millisecond, Timeout: 2 * time.Second}
}

func TestWaitForOutcome_StartReachesRunning(t *testing.T) {
	var calls int

	get := scriptedGet([]*client.Application{
		waitApp(client.StatusInStart, client.BuildStatusBuilt),
		waitApp(client.StatusRunning, client.BuildStatusBuilt),
	}, &calls)

	got, err := waitForOutcome(context.Background(), get, waitStart, fastOpts())
	if err != nil {
		t.Fatalf("waitForOutcome: %v", err)
	}

	if got.Status != client.StatusRunning {
		t.Errorf("status = %s, want running", got.Status)
	}

	if calls < 2 {
		t.Errorf("calls = %d, want >= 2 (transient observed)", calls)
	}
}

func TestWaitForOutcome_StopReachesStopped(t *testing.T) {
	var calls int

	get := scriptedGet([]*client.Application{
		waitApp(client.StatusInStop, client.BuildStatusBuilt),
		waitApp(client.StatusStopped, client.BuildStatusBuilt),
	}, &calls)

	got, err := waitForOutcome(context.Background(), get, waitStop, fastOpts())
	if err != nil {
		t.Fatalf("waitForOutcome: %v", err)
	}

	if got.Status != client.StatusStopped {
		t.Errorf("status = %s, want stopped", got.Status)
	}
}

func TestWaitForOutcome_StartFailureSettles(t *testing.T) {
	var calls int

	get := scriptedGet([]*client.Application{waitApp(client.StatusFailure, client.BuildStatusBuilt)}, &calls)

	got, err := waitForOutcome(context.Background(), get, waitStart, fastOpts())
	if err != nil {
		t.Fatalf("waitForOutcome: %v", err)
	}

	if got.Status != client.StatusFailure {
		t.Errorf("status = %s, want failure (settled so the command can report it)", got.Status)
	}
}

func TestWaitForOutcome_BuildBuildingThenBuilt(t *testing.T) {
	var calls int

	get := scriptedGet([]*client.Application{
		waitApp(client.StatusDeploying, client.BuildStatusBuilding),
		waitApp(client.StatusDeploying, client.BuildStatusBuilt),
	}, &calls)

	got, err := waitForOutcome(context.Background(), get, waitBuild, fastOpts())
	if err != nil {
		t.Fatalf("waitForOutcome: %v", err)
	}

	if got.BuildStatus != client.BuildStatusBuilt {
		t.Errorf("build = %s, want built", got.BuildStatus)
	}

	if calls < 2 {
		t.Errorf("calls = %d, want >= 2 (building observed before built)", calls)
	}
}

func TestWaitForOutcome_BuildFastTerminalEdge(t *testing.T) {
	// This build settles before the first poll: build_status is already terminal
	// and `building` never appears. After the grace window, the wait must accept
	// the terminal status instead of hanging.
	var calls int

	get := scriptedGet([]*client.Application{waitApp(client.StatusRunning, client.BuildStatusBuilt)}, &calls)

	start := time.Now()

	got, err := waitForOutcome(context.Background(), get, waitBuild, fastOpts())
	if err != nil {
		t.Fatalf("waitForOutcome: %v", err)
	}

	if got.BuildStatus != client.BuildStatusBuilt {
		t.Errorf("build = %s, want built", got.BuildStatus)
	}
	// The wait must last about the grace window before it accepts.
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Errorf("accepted after %v, want >= grace window", elapsed)
	}
}

func TestWaitForOutcome_BuildNoneNeverSettles(t *testing.T) {
	// `none` means the build never started, so it is not an outcome. The wait
	// must time out instead of reporting the build as complete.
	var calls int

	get := scriptedGet([]*client.Application{waitApp(client.StatusRunning, client.BuildStatusNone)}, &calls)

	_, err := waitForOutcome(context.Background(), get, waitBuild, waitOptions{
		Interval: time.Millisecond,
		Grace:    5 * time.Millisecond,
		Timeout:  40 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("want a timeout error, got a settled build")
	}
}

func TestWaitForOutcome_RestartIgnoresStaleRunning(t *testing.T) {
	// The first poll can still show the `running` from before the restart. To
	// settle on it would report success without seeing anything, so the wait must
	// hold until the restart disrupts the status.
	var calls int

	get := scriptedGet([]*client.Application{
		waitApp(client.StatusRunning, client.BuildStatusBuilt),
		waitApp(client.StatusRestarting, client.BuildStatusBuilt),
		waitApp(client.StatusRunning, client.BuildStatusBuilt),
	}, &calls)

	got, err := waitForOutcome(context.Background(), get, waitRestart, waitOptions{
		Interval: time.Millisecond,
		Grace:    time.Hour, // out of reach: only the observed sequence can settle this
		Timeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("waitForOutcome: %v", err)
	}

	if got.Status != client.StatusRunning {
		t.Errorf("status = %s, want running", got.Status)
	}

	if calls < 3 {
		t.Errorf("calls = %d, want >= 3 (running -> restarting -> running observed)", calls)
	}
}

func TestWaitForOutcome_RestartFastEdgeWaitsForGrace(t *testing.T) {
	// This in-place restart is too quick to see: the status never leaves
	// `running`. The grace window is the only way out, and the wait must sit
	// through it.
	var calls int

	get := scriptedGet([]*client.Application{waitApp(client.StatusRunning, client.BuildStatusBuilt)}, &calls)

	start := time.Now()

	got, err := waitForOutcome(context.Background(), get, waitRestart, fastOpts())
	if err != nil {
		t.Fatalf("waitForOutcome: %v", err)
	}

	if got.Status != client.StatusRunning {
		t.Errorf("status = %s, want running", got.Status)
	}

	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Errorf("accepted after %v, want >= grace window", elapsed)
	}
}

func TestWaitForOutcome_RestartFailureSettles(t *testing.T) {
	var calls int

	get := scriptedGet([]*client.Application{waitApp(client.StatusFailure, client.BuildStatusBuilt)}, &calls)

	got, err := waitForOutcome(context.Background(), get, waitRestart, fastOpts())
	if err != nil {
		t.Fatalf("waitForOutcome: %v", err)
	}

	if got.Status != client.StatusFailure {
		t.Errorf("status = %s, want failure (settled so the command can report it)", got.Status)
	}
}

func TestWaitForOutcome_TimeoutCancelsInFlightPoll(t *testing.T) {
	// The timeout must reach the poll itself, not only the gap between polls.
	polling := make(chan struct{})
	get := func(ctx context.Context) (*client.Application, error) {
		close(polling)
		<-ctx.Done()
		// A cancelled poll reports what it has, and the wait discards it.
		return waitApp(client.StatusInStart, client.BuildStatusBuilt), ctx.Err()
	}

	done := make(chan error, 1)

	go func() {
		_, err := waitForOutcome(context.Background(), get, waitStart, waitOptions{
			Interval: time.Millisecond,
			Timeout:  30 * time.Millisecond,
		})
		done <- err
	}()

	<-polling

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want an error from the cancelled poll")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitForOutcome hung: the timeout did not reach the in-flight poll")
	}
}

func TestWaitForOutcome_Timeout(t *testing.T) {
	var calls int

	get := scriptedGet([]*client.Application{waitApp(client.StatusInStart, client.BuildStatusBuilt)}, &calls) // never reaches running

	_, err := waitForOutcome(context.Background(), get, waitStart, waitOptions{
		Interval: time.Millisecond,
		Timeout:  30 * time.Millisecond,
	})

	// The wait's own timer must report the sentinel, so the command can tell
	// this timeout apart from a poll's own deadline error.
	if !errors.Is(err, errWaitTimeout) {
		t.Fatalf("err = %v, want errWaitTimeout", err)
	}
}

func TestWaitForOutcome_PollDeadlineErrorIsNotTimeout(t *testing.T) {
	// An HTTP client timeout inside a poll also wraps context.DeadlineExceeded.
	// It must surface as the poll's own error, never as the wait timeout.
	reqErr := fmt.Errorf("Get \"/apps/app_x\": %w", context.DeadlineExceeded)
	get := func(context.Context) (*client.Application, error) { return nil, reqErr }

	_, err := waitForOutcome(context.Background(), get, waitStart, waitOptions{
		Interval: time.Millisecond,
		Timeout:  time.Hour, // the timer stays far out of reach
	})

	if errors.Is(err, errWaitTimeout) {
		t.Fatalf("err = %v, must not be the wait timeout", err)
	}

	if !errors.Is(err, reqErr) {
		t.Fatalf("err = %v, want the poll's request error", err)
	}
}

func TestWaitForOutcome_ContextCancel(t *testing.T) {
	var calls int

	get := scriptedGet([]*client.Application{waitApp(client.StatusInStart, client.BuildStatusBuilt)}, &calls)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()

	_, err := waitForOutcome(ctx, get, waitStart, waitOptions{Interval: time.Millisecond})
	if err == nil {
		t.Fatal("want error on context cancel")
	}
}
