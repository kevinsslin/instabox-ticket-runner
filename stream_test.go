package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEventWaitDoesNotPollWhileConnected(t *testing.T) {
	var checks atomic.Int32
	attached := make(chan struct{})
	ended := make(chan struct{})
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		done <- awaitCompletion(ctx, func() (bool, error) { return checks.Add(1) > 1, nil }, func(ctx context.Context) error {
			close(attached)
			select {
			case <-ended:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}, streamSleep)
	}()
	<-attached
	// Synchronized attachment proves only the initial read ran. No completion
	// inspection happens again until follow ends, regardless of transcript size.
	if checks.Load() != 1 {
		t.Fatal("checked while following", checks.Load())
	}
	close(ended)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("EOF did not wake completion")
	}
	if checks.Load() != 2 {
		t.Fatal("unexpected remote polling", checks.Load())
	}
}
func TestEventEOFRequiresReconciliationAndBackoff(t *testing.T) {
	checks, follows, sleeps := 0, 0, 0
	err := awaitCompletion(context.Background(), func() (bool, error) { checks++; return checks == 4, nil }, func(context.Context) error { follows++; return errors.New("connection lost") }, func(context.Context, time.Duration) error { sleeps++; return nil })
	if err != nil || follows != 2 || checks != 4 || sleeps != 1 {
		t.Fatal(err, follows, checks, sleeps)
	}
}
func TestFailedFollowExitCanStillRepresentFinishedFailedTurn(t *testing.T) {
	checks := 0
	err := awaitCompletion(context.Background(), func() (bool, error) { checks++; return checks == 2, nil }, func(context.Context) error { return errors.New("agent exited 1") }, func(context.Context, time.Duration) error { t.Fatal("completed turn must not back off"); return nil })
	if err != nil {
		t.Fatal(err)
	}
}
func TestEventDisconnectRecoveryIsBoundedAndDoesNotInventCompletion(t *testing.T) {
	follows := 0
	delays := []time.Duration{}
	err := awaitCompletion(context.Background(), func() (bool, error) { return false, nil }, func(context.Context) error { follows++; return nil }, func(_ context.Context, d time.Duration) error { delays = append(delays, d); return nil })
	if err == nil || follows != 6 || len(delays) != 5 {
		t.Fatal(err, follows, delays)
	}
	for n, d := range delays {
		expected := time.Second * time.Duration(1<<uint(n))
		if d < expected*8/10 || d > expected*12/10 {
			t.Fatal("backoff out of bounds", delays)
		}
	}
}
func TestEventCancellationLeavesWorkRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	checks := 0
	err := awaitCompletion(ctx, func() (bool, error) { checks++; return false, nil }, func(ctx context.Context) error { cancel(); return ctx.Err() }, streamSleep)
	if !errors.Is(err, context.Canceled) || checks != 1 {
		t.Fatal(err, checks)
	}
}
func TestEventReconnectRechecksStateWithoutFollowingUnknownJob(t *testing.T) {
	calls, follow := 0, 0
	err := awaitCompletion(context.Background(), func() (bool, error) {
		calls++
		if calls == 1 {
			return false, errReconcileRetry
		}
		return true, nil
	}, func(context.Context) error { follow++; return nil }, func(context.Context, time.Duration) error { return nil })
	if err != nil || calls != 2 || follow != 0 {
		t.Fatal(err, calls, follow)
	}
}
func TestEventWaitPreservesFatalStateErrors(t *testing.T) {
	fatal := errors.New("credential read-back failed")
	if err := awaitCompletion(context.Background(), func() (bool, error) { return false, fatal }, func(context.Context) error { t.Fatal("followed after fatal error"); return nil }, streamSleep); !errors.Is(err, fatal) {
		t.Fatal(err)
	}
}
func TestNativeFollowStreamsWithoutJSONBufferOrShortTimeout(t *testing.T) {
	argsFile := fakeInstabox(t, "printf '%s\\n' \"$@\" > \"$0.args\"\nprintf '%s\\n' '{\"type\":\"instabox.turn_end\",\"exitCode\":1}'\nexit 1\n")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// Failure exit is intentionally not treated as completion by this transport.
	if err := (Instabox{Timeout: time.Nanosecond, MaxOutput: 1}).Follow(ctx, "owned-box", "conv-one"); err == nil || ctx.Err() != nil {
		t.Fatal(err, ctx.Err())
	}
	raw, err := os.ReadFile(filepath.Join(argsFile, "instabox.args"))
	if err != nil {
		t.Fatal(err)
	}
	args := string(raw)
	if !strings.Contains(args, "-f\n") || strings.Contains(args, "--json") || strings.Contains(args, "interrupt") {
		t.Fatal(args)
	}
}
func TestTurnStreamsLimitAndCancelWaiters(t *testing.T) {
	streams := newTurnStreams(context.Background())
	for _, key := range []string{"a", "b", "c", "d"} {
		if !streams.start(key, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }) {
			t.Fatal("missing slot")
		}
	}
	if streams.start("a", func(context.Context) error { return nil }) || streams.start("fifth", func(context.Context) error { return nil }) {
		t.Fatal("duplicate or excess follower")
	}
	streams.close()
}

func TestNativeFollowerCancellationOnlyStopsLocalFollower(t *testing.T) {
	fakeInstabox(t, "exec sleep 10\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (Instabox{}).Follow(ctx, "owned-box", "conv-one"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestEventFollowingUsesSameCompletionSafetyChecks(t *testing.T) {
	o, c := testObserver(t)
	client := &linearEventClient{observationClient: c, job: "job-one", status: "running"}
	checkpoints := 0
	watcher := supervisor{state: o.state, client: client, checkpoint: func(string) error { checkpoints++; return nil }}
	turn := supervisedTurn{Task: "TASK-1", Conversation: "conv-one", Job: "job-one"}
	follows := 0
	err := awaitCompletion(context.Background(), func() (bool, error) { return watcher.inspect(turn) }, func(context.Context) error {
		follows++
		client.status = "idle"
		return errors.New("follow process exit")
	}, streamSleep)
	if err != nil || follows != 1 || checkpoints != 1 {
		t.Fatal(err, follows, checkpoints)
	}
}

func TestSlowReconciliationCoversStalledStreamWithoutReconnecting(t *testing.T) {
	attached := make(chan struct{})
	canceled := make(chan struct{})
	checks := 0
	done, err := followConnected(context.Background(), func() (bool, error) { checks++; return true, nil }, func(ctx context.Context) error {
		close(attached)
		<-ctx.Done()
		close(canceled)
		return ctx.Err()
	}, time.Millisecond)
	if err != nil || !done || checks != 1 {
		t.Fatal(done, err, checks)
	}
	select {
	case <-attached:
	default:
		t.Fatal("never attached")
	}
	select {
	case <-canceled:
	default:
		t.Fatal("follower leaked after reconciliation")
	}
}
