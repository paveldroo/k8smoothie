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

const kickAnnotation = "last-activated"

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
	Client    kube.Client
	Namespace string
	Frequency time.Duration
	Out       io.Writer
}

// WaitAll watches every named Deployment concurrently and returns results in input order.
func (w Watcher) WaitAll(ctx context.Context, names []string) []Result {
	type indexed struct {
		i int
		r Result
	}
	w.Out = &syncWriter{w: w.Out}
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
func (w Watcher) Watch(ctx context.Context, name string) Result {
	out := w.Out
	if out == nil {
		out = io.Discard
	}
	logger := log.New(out, "["+name+"] ", log.Ltime)
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

	warned := false
	for {
		d, v, err := w.check(ctx, name)
		if err != nil {
			if ctx.Err() != nil {
				return fromCtx()
			}
			return finish(Failed, err.Error())
		}
		if v.Warning != "" && !warned {
			logger.Printf("⚠️ %s", v.Warning)
			warned = true
		}

		switch v.State {
		case StateDone:
			return finish(Succeeded, v.Reason)
		case StateFailed:
			return finish(Failed, v.Reason)
		case StateKick:
			logger.Printf("🥾 %s, let's kick the deployment a little", v.Reason)
			if err := w.Client.Annotate(ctx, w.Namespace, name, kickAnnotation, time.Now().Format(time.RFC3339)); err != nil {
				if ctx.Err() != nil {
					return fromCtx()
				}
				return finish(Failed, fmt.Sprintf("kick: %s", err))
			}
		default:
			logger.Printf("🤔 %s", v.Reason)
		}

		st := d.Status
		logger.Printf("⏳ updated/available/replicas: %d/%d/%d of %d desired", st.UpdatedReplicas, st.AvailableReplicas, st.Replicas, d.DesiredReplicas())

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
	return d, Evaluate(d, rs, pods, time.Now()), nil
}
