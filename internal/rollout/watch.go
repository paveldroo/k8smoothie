package rollout

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/paveldroo/k8smoothie/internal/kube"
)

var ErrNoDeployments = errors.New("no deployments found")

const (
	kickAnnotation       = "last-activated"
	MaxConsecutiveErrors = 3
	failConfirmations    = 2
)

type Status int

const (
	Succeeded Status = iota
	Failed
	TimedOut
	Canceled
)

func (s Status) String() string {
	switch s {
	case Succeeded:
		return "succeeded"
	case Failed:
		return "failed"
	case TimedOut:
		return "timed out"
	default:
		return "canceled"
	}
}

// Emoji returns the summary marker for s.
func (s Status) Emoji() string {
	switch s {
	case Succeeded:
		return "✅"
	case Failed:
		return "💥"
	case TimedOut:
		return "⏰"
	default:
		return "🛑"
	}
}

// Result is the final outcome of watching one Deployment.
type Result struct {
	Name     string
	Status   Status
	Reason   string
	Duration time.Duration
}

// Watcher waits for Deployment rollouts in one namespace, kicking stuck ones.
type Watcher struct {
	Client       kube.Client
	Namespace    string
	Frequency    time.Duration
	KickInterval time.Duration
	Out          io.Writer
	prefixed     bool
}

// WaitAll watches every named Deployment concurrently and returns results in input order.
func (w Watcher) WaitAll(ctx context.Context, names []string) []Result {
	type indexed struct {
		i int
		r Result
	}
	w.Out = &syncWriter{w: w.Out}
	w.prefixed = len(names) > 1
	ch := make(chan indexed, len(names))
	for i, name := range names {
		go func() {
			ch <- indexed{i, w.Watch(ctx, name)}
		}()
	}
	results := make([]Result, len(names))
	for range names {
		x := <-ch
		results[x.i] = x.r
	}
	return results
}

// Watch polls one Deployment until it is rolled out, fails, or ctx ends.
// The same failure must be seen on consecutive checks and kubectl errors must
// occur MaxConsecutiveErrors times in a row before the watch fails.
// Kicks are sent at most once per KickInterval; a failed kick is only logged.
func (w Watcher) Watch(ctx context.Context, name string) Result {
	out := w.Out
	if out == nil {
		out = io.Discard
	}
	prefix := ""
	if w.prefixed {
		prefix = "[" + name + "] "
	}
	logger := log.New(out, prefix, log.Ltime)
	start := time.Now()
	finish := func(s Status, reason string) Result {
		logger.Printf("%s %s: %s", s.Emoji(), s, reason)
		return Result{Name: name, Status: s, Reason: reason, Duration: time.Since(start)}
	}
	fromCtx := func() Result {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return finish(TimedOut, "timeout reached")
		}
		return finish(Canceled, "canceled")
	}

	var (
		warned     bool
		errStreak  int
		failStreak int
		failKey    string
		lastKick   time.Time
	)
	for {
		d, v, err := w.check(ctx, name)
		if err != nil {
			if ctx.Err() != nil {
				return fromCtx()
			}
			failStreak, failKey = 0, ""
			errStreak++
			if errStreak >= MaxConsecutiveErrors {
				return finish(Failed, err.Error())
			}
		} else {
			errStreak = 0
			if v.Warning != "" && !warned {
				logger.Printf("⚠️ %s", v.Warning)
				warned = true
			}
			if v.State == StateFailed && v.Key == failKey {
				failStreak++
			} else if v.State == StateFailed {
				failStreak, failKey = 1, v.Key
			} else {
				failStreak, failKey = 0, ""
			}

			switch v.State {
			case StateDone:
				return finish(Succeeded, v.Reason)
			case StateFailed:
				if failStreak >= failConfirmations {
					return finish(Failed, v.Reason)
				}
			case StateKick:
				if !lastKick.IsZero() && time.Since(lastKick) < w.KickInterval {
					break
				}
				logger.Printf("🥾 let's kick the deployment a little")
				if kerr := w.Client.Annotate(ctx, w.Namespace, name, kickAnnotation, time.Now().Format(time.RFC3339Nano)); kerr != nil {
					if ctx.Err() != nil {
						return fromCtx()
					}
					logger.Printf("🙈 kick failed, still watching: %s", kerr)
				} else {
					lastKick = time.Now()
				}
			}
			logger.Printf("⏳ %d of %d pods updated, rollout in progress...", d.Status.UpdatedReplicas, d.DesiredReplicas())
		}

		timer := time.NewTimer(w.Frequency)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fromCtx()
		case <-timer.C:
		}
	}
}

type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w == nil {
		return len(p), nil
	}
	return s.w.Write(p)
}

func (w Watcher) check(ctx context.Context, name string) (kube.Deployment, Verdict, error) {
	d, err := w.Client.GetDeployment(ctx, w.Namespace, name)
	if err != nil {
		return d, Verdict{}, fmt.Errorf("get deployment: %w", err)
	}
	sel := d.Spec.Selector.MatchLabels
	rs, err := w.Client.ListReplicaSets(ctx, w.Namespace, sel)
	if err != nil {
		return d, Verdict{}, fmt.Errorf("list replicasets: %w", err)
	}
	pods, err := w.Client.ListPods(ctx, w.Namespace, sel)
	if err != nil {
		return d, Verdict{}, fmt.Errorf("list pods: %w", err)
	}
	return d, Evaluate(d, rs, pods), nil
}
