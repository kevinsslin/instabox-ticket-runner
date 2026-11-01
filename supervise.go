package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

type supervisedTurn struct{ Task, Conversation, Job, Mode, Status string }
type supervisor struct {
	state      *State
	client     Client
	checkpoint func(string) error
}

func runSupervisor(root, config, action string, args []string) error {
	f := flag.NewFlagSet(action, flag.ContinueOnError)
	interval := f.Duration("interval", 15*time.Second, "poll interval (5s to 1m)")
	reordered, e := reorder(args)
	if e != nil {
		return e
	}
	if e = f.Parse(reordered); e != nil {
		return e
	}
	if *interval < 5*time.Second || *interval > time.Minute {
		return errors.New("interval must be 5s to 1m")
	}
	task := ""
	if action == "watch" {
		task, e = requireArg(f.Args())
		if e != nil {
			return e
		}
	} else if len(f.Args()) != 0 {
		return errors.New("unexpected supervisor arguments")
	}
	// Release dispatch's lock before waiting. The daemon has its own process lock.
	s, e := openState(expandHome(root))
	if e != nil {
		return e
	}
	root = s.Root
	s.Close()
	lock := flock.New(filepath.Join(root, "supervisor.lock"))
	ok, e := lock.TryLock()
	if e != nil || !ok {
		return errors.New("supervisor already running; it will collect host-managed turns")
	}
	defer lock.Unlock()
	s, e = openObservationState(root)
	if e != nil {
		return e
	}
	defer s.DB.Close()
	watcher := supervisor{state: s, client: Instabox{Timeout: 20 * time.Second, MaxOutput: 8 << 20}, checkpoint: func(id string) error {
		argv := []string{"--state", root}
		if config != "" {
			argv = append(argv, "--config", config)
		}
		_, e := execute(append(argv, "checkpoint", id))
		return e
	}}
	// Writes use a short normal controller lock; no database writes bypass it.
	fmt.Fprintln(os.Stderr, "Supervisor active: completed turns will be checkpointed; errors preserve work and leases.")
	for {
		turns, e := watcher.turns(task)
		if e != nil {
			return e
		}
		if task != "" && len(turns) == 0 {
			return errors.New("no recorded turn; inspect the task and recover the original job first")
		}
		for _, turn := range turns {
			if task == "" && (turn.Mode != "host" || turn.Status != "active") {
				continue
			}
			done, err := watcher.inspect(turn)
			if err != nil {
				if recordTurnStatus(root, turn, "attention", "Automatic checkpoint needs attention; inspect controller status, native events and credentials.") == nil {
					fmt.Fprintf(os.Stderr, "Task %s needs attention; work and lease were preserved.\n", turn.Task)
				}
				if task != "" {
					return err
				}
				continue
			}
			if done {
				if e = recordTurnStatus(root, turn, "done", ""); e != nil {
					if task != "" {
						return e
					}
					continue
				}
				fmt.Fprintf(os.Stderr, "Task %s checkpointed and paused.\n", turn.Task)
				if task != "" {
					return nil
				}
			}
		}
		time.Sleep(*interval)
	}
}
func (s *supervisor) turns(task string) ([]supervisedTurn, error) {
	query := "SELECT ticket,conversation,job,mode,status FROM turns"
	args := []any{}
	if task != "" {
		query += " WHERE ticket=?"
		args = append(args, task)
	} else {
		query += " WHERE mode='host' AND status='active'"
	}
	rows, e := s.state.DB.Query(query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	turns := []supervisedTurn{}
	for rows.Next() {
		var v supervisedTurn
		if e = rows.Scan(&v.Task, &v.Conversation, &v.Job, &v.Mode, &v.Status); e != nil {
			return nil, e
		}
		turns = append(turns, v)
	}
	return turns, rows.Err()
}
func (s *supervisor) inspect(turn supervisedTurn) (bool, error) {
	t, e := s.state.Task(turn.Task)
	if e != nil {
		return false, e
	}
	if t.Conversation != turn.Conversation || turn.Job == "" {
		return false, errors.New("recorded turn mismatch; reconcile before checkpoint")
	}
	if t.Phase == "paused" {
		return true, nil
	}
	if t.Phase == "auth-saved" || t.Phase == "credentials-removed" {
		return true, s.checkpoint(t.Ticket)
	}
	if t.Phase != "active" {
		return false, errors.New("task phase requires recovery")
	}
	r := Runner{State: s.state, Client: s.client}
	b, e := r.owned(t)
	if e != nil {
		return false, e
	}
	// Idle suspension can occur while the model waits. Polling must not mistake
	// cached process state for completion. Explicit watch can be retried after recovery.
	if b.State != "running" {
		return false, errors.New("VM paused or stopped before checkpoint; inspect and activate the existing task")
	}
	var events struct {
		Job      string `json:"jobId"`
		Status   string `json:"status"`
		ExitCode *int   `json:"exitCode"`
	}
	if e = s.client.Call(&events, "agent", "events", t.Box, "-c", turn.Conversation); e != nil {
		return false, e
	}
	if events.Job != turn.Job {
		return false, nil
	}
	if events.Status == "running" {
		return false, nil
	}
	if !idleConversation(events.Status) || events.ExitCode == nil {
		return false, errors.New("agent completion is unknown; preserve lease")
	}
	// Checkpoint rechecks all conversations, jobs and processes under its normal
	// lock, captures the final view, then saves credentials and pauses.
	if e = s.checkpoint(t.Ticket); e != nil {
		if e.Error() == "controller busy" {
			return false, nil
		}
		return false, e
	}
	return true, nil
}
func recordTurnStatus(root string, turn supervisedTurn, status, note string) error {
	s, e := openState(root)
	if e != nil {
		return e
	}
	defer s.Close()
	_, e = s.DB.Exec("UPDATE turns SET status=?,note=? WHERE ticket=? AND conversation=? AND job=?", status, note, turn.Task, turn.Conversation, turn.Job)
	return e
}

func supervisorPresent(root string) bool {
	lock := flock.New(filepath.Join(root, "supervisor.lock"))
	ok, e := lock.TryLock()
	if e != nil {
		return false
	}
	if ok {
		lock.Unlock()
		return false
	}
	return true
}
