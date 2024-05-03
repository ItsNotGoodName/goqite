// Package jobs provides a [Runner] which can run registered job [Func]s by name, when a message for it is received
// on the underlying queue.
//
// It provides:
// - Limit on how many jobs can be run simultaneously
// - Automatic message timeout extension while the job is running
// - Graceful shutdown
package jobs

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/maragudk/goqite"
)

type NewRunnerOpts struct {
	Limit        int
	Log          logger
	PollInterval time.Duration
	Queue        *goqite.Queue
}

func NewRunner(opts NewRunnerOpts) *Runner {
	if opts.Log == nil {
		opts.Log = &discardLogger{}
	}

	if opts.Limit == 0 {
		opts.Limit = runtime.GOMAXPROCS(0)
	}

	if opts.PollInterval == 0 {
		opts.PollInterval = 100 * time.Millisecond
	}

	return &Runner{
		jobCountLimit: opts.Limit,
		jobs:          make(map[string]Func),
		log:           opts.Log,
		pollInterval:  opts.PollInterval,
		queue:         opts.Queue,
	}
}

type Runner struct {
	jobCount      int
	jobCountLimit int
	jobCountLock  sync.RWMutex
	jobs          map[string]Func
	log           logger
	pollInterval  time.Duration
	queue         *goqite.Queue
}

type message struct {
	Name    string
	Message []byte
}

// Start the Runner, blocking until the given context is cancelled.
// When the context is cancelled, waits for the jobs to finish.
func (r *Runner) Start(ctx context.Context) {
	var names []string
	for k := range r.jobs {
		names = append(names, k)
	}
	sort.Strings(names)

	r.log.Info("Starting", "jobs", names)

	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			r.log.Info("Stopping")
			wg.Wait()
			r.log.Info("Stopped")
			return
		default:
			r.receiveAndRun(ctx, &wg)
		}
	}
}

func (r *Runner) receiveAndRun(ctx context.Context, wg *sync.WaitGroup) {
	r.jobCountLock.RLock()
	if r.jobCount == r.jobCountLimit {
		r.jobCountLock.RUnlock()
		// This is to avoid a busy loop
		time.Sleep(r.pollInterval)
		return
	} else {
		r.jobCountLock.RUnlock()
	}

	m, err := r.queue.ReceiveAndWait(ctx, r.pollInterval)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return
		}
		r.log.Info("Error receiving job", "error", err)
		// Sleep a bit to not hammer the queue if there's an error with it
		time.Sleep(time.Second)
		return
	}

	if m == nil {
		return
	}

	var jm message
	if err := gob.NewDecoder(bytes.NewReader(m.Body)).Decode(&jm); err != nil {
		r.log.Info("Error decoding job message body", "error", err)
		return
	}

	job, ok := r.jobs[jm.Name]
	if !ok {
		panic(fmt.Sprintf(`job "%v" not registered`, jm.Name))
	}

	r.jobCountLock.Lock()
	r.jobCount++
	r.jobCountLock.Unlock()

	wg.Add(1)
	go func() {
		defer wg.Done()

		defer func() {
			r.jobCountLock.Lock()
			r.jobCount--
			r.jobCountLock.Unlock()
		}()

		defer func() {
			if rec := recover(); rec != nil {
				r.log.Info("Recovered from panic in job", "error", rec)
			}
		}()

		jobCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Extend the job message while the job is running
		done := make(chan struct{}, 1)
		defer func() {
			done <- struct{}{}
		}()

		go func() {
			for {
				select {
				case <-done:
					return
				default:
					if err := r.queue.Extend(jobCtx, m.ID, 5*time.Second); err != nil {
						r.log.Info("Error extending message timeout", "error", err)
					}
					time.Sleep(3 * time.Second)
				}
			}
		}()

		before := time.Now()
		if err := job(jobCtx, jm.Message); err != nil {
			r.log.Info("Error running job", "name", jm.Name, "error", err)
			return
		}
		duration := time.Since(before)
		r.log.Info("Ran job", "name", jm.Name, "duration", duration)

		deleteCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := r.queue.Delete(deleteCtx, m.ID); err != nil {
			r.log.Info("Error deleting job from queue", "error", err)
		}
	}()
}

// Func is a job to be done. It gets the message m from the queue.
type Func func(ctx context.Context, m []byte) error

func (r *Runner) Register(name string, job Func) {
	if _, ok := r.jobs[name]; ok {
		panic(fmt.Sprintf(`job "%v" already registered`, name))
	}
	r.jobs[name] = job
}

func Create(ctx context.Context, q *goqite.Queue, name string, m []byte) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(message{Name: name, Message: m}); err != nil {
		return err
	}
	return q.Send(ctx, goqite.Message{Body: buf.Bytes()})
}

// logger matches the info level method from the slog.Logger.
type logger interface {
	Info(msg string, args ...any)
}

type discardLogger struct{}

func (d *discardLogger) Info(msg string, args ...any) {}
