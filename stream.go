package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

var errReconcileRetry = errors.New("native status temporarily unavailable")

type nativeTurn struct {
	Job, Status string
	ExitCode    *int
}

// Small metadata reads at attachment/reconnection avoid replaying large logs.
func readNativeTurn(client Client, t Task, job string) (nativeTurn, error) {
	var list struct {
		Conversations []Conversation `json:"conversations"`
	}
	if err := client.Call(&list, "agent", "ls", t.Box); err != nil {
		return nativeTurn{}, fmt.Errorf("%w: cannot read conversations", errReconcileRetry)
	}
	for _, c := range list.Conversations {
		if c.ID != t.Conversation {
			continue
		}
		if c.Provider != t.Provider {
			return nativeTurn{}, errors.New("native conversation provider mismatch")
		}
		if c.Job != job {
			return nativeTurn{}, fmt.Errorf("%w: current job does not match recorded turn", errReconcileRetry)
		}
		if c.Status != "running" && (!idleConversation(c.Status) || c.ExitCode == nil) {
			return nativeTurn{}, errors.New("native completion is unknown; preserve work and lease")
		}
		return nativeTurn{c.Job, c.Status, c.ExitCode}, nil
	}
	return nativeTurn{}, fmt.Errorf("%w: recorded conversation not visible", errReconcileRetry)
}

// An event stream accelerates notification. Durable state and authoritative
// native checks decide completion. Disconnects never dispatch another prompt.
func awaitCompletion(ctx context.Context, check func() (bool, error), follow func(context.Context) error, sleep func(context.Context, time.Duration) error) error {
	failures := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		done, err := check()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil && done {
			return nil
		}
		if err != nil && !errors.Is(err, errReconcileRetry) {
			return err
		}
		if err == nil {
			connected := time.Now()
			// A failed agent turn can make the follow CLI exit non-zero too.
			// Always reconcile its exact job rather than interpreting the CLI exit.
			reconciled, streamErr := followConnected(ctx, check, follow, 5*time.Minute)
			if reconciled && streamErr == nil {
				return nil
			}
			if streamErr != nil && !errors.Is(streamErr, errReconcileRetry) {
				return streamErr
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			done, err = check()
			if err == nil && done {
				return nil
			}
			if err != nil && !errors.Is(err, errReconcileRetry) {
				return err
			}
			if time.Since(connected) >= 30*time.Second {
				failures = 0
			}
		}
		failures++
		if failures >= 6 {
			return errors.New("event stream or native status unavailable after bounded recovery; preserve work and lease")
		}
		delay := time.Second * time.Duration(1<<uint(failures-1))
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
		delay = time.Duration(float64(delay) * (0.8 + rand.Float64()*0.4))
		if err = sleep(ctx, delay); err != nil {
			return err
		}
	}
}

// A slow safety reconciliation covers a half-open or stalled CLI stream.
// Healthy followers stay attached; no transcript is replayed on each tick.
func followConnected(ctx context.Context, check func() (bool, error), follow func(context.Context) error, interval time.Duration) (bool, error) {
	child, cancel := context.WithCancel(ctx)
	ended := make(chan struct{})
	go func() { defer close(ended); _ = follow(child) }()
	defer func() { cancel(); <-ended }()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-ended:
			return false, nil
		case <-ticker.C:
			done, err := check()
			if done || err != nil {
				return done, err
			}
		}
	}
}

func streamSleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type streamResult struct {
	Key string
	Err error
}

// The owner loop alone reads/writes active. Workers report through results.
// Bound concurrent followers without a queue service or a goroutine per receipt.
type turnStreams struct {
	ctx     context.Context
	cancel  context.CancelFunc
	active  map[string]bool
	results chan streamResult
	wg      sync.WaitGroup
}

func newTurnStreams(ctx context.Context) *turnStreams {
	ctx, cancel := context.WithCancel(ctx)
	return &turnStreams{ctx: ctx, cancel: cancel, active: map[string]bool{}, results: make(chan streamResult, 4)}
}
func (s *turnStreams) start(key string, run func(context.Context) error) bool {
	if s.active[key] || len(s.active) >= 4 {
		return false
	}
	s.active[key] = true
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		err := run(s.ctx)
		select {
		case s.results <- streamResult{key, err}:
		case <-s.ctx.Done():
		}
	}()
	return true
}
func (s *turnStreams) close() { s.cancel(); s.wg.Wait() }
