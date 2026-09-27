package rollout

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/paveldroo/k8smoothie/internal/kube"
)

type State int

const (
	StateWaiting State = iota
	StateDone
	StateKick
	StateFailed
)

func (s State) String() string {
	switch s {
	case StateDone:
		return "done"
	case StateKick:
		return "kick"
	case StateFailed:
		return "failed"
	default:
		return "waiting"
	}
}

// Verdict is the outcome of one evaluation of a Deployment rollout.
type Verdict struct {
	State   State
	Reason  string
	Warning string
}

var fatalWaitingReasons = map[string]bool{
	"CrashLoopBackOff":           true,
	"ImagePullBackOff":           true,
	"InvalidImageName":           true,
	"CreateContainerConfigError": true,
}

// Evaluate decides the rollout state of d from its ReplicaSets and pods.
// rsList and pods may contain unrelated objects; ownership is checked by uid.
// Pods past their deletion grace period no longer count as terminating.
func Evaluate(d kube.Deployment, rsList []kube.ReplicaSet, pods []kube.Pod, now time.Time) Verdict {
	v := Verdict{Warning: progressWarning(d)}
	st := d.Status

	if st.ObservedGeneration < d.Metadata.Generation {
		v.Reason = fmt.Sprintf("waiting for controller to observe generation %d (observed %d)", d.Metadata.Generation, st.ObservedGeneration)
		return v
	}

	if st.UpdatedReplicas == d.DesiredReplicas() && st.Replicas == st.UpdatedReplicas && st.AvailableReplicas == st.UpdatedReplicas {
		v.State = StateDone
		v.Reason = fmt.Sprintf("%d of %d replicas updated and available", st.AvailableReplicas, d.DesiredReplicas())
		return v
	}

	if d.Spec.Paused {
		v.State = StateFailed
		v.Reason = "deployment is paused"
		return v
	}

	revision := d.Metadata.Annotations[kube.AnnotationRevision]
	owned := map[string]bool{}
	current := ""
	for _, rs := range rsList {
		if !rs.Metadata.OwnedBy(d.Metadata.UID) {
			continue
		}
		owned[rs.Metadata.UID] = true
		if revision != "" && rs.Metadata.Annotations[kube.AnnotationRevision] == revision {
			current = rs.Metadata.UID
		}
	}

	var terminating, pending, terminal, old, stale int
	for _, p := range pods {
		if !ownedByAny(p.Metadata, owned) {
			continue
		}
		if p.Status.Phase == kube.PodFailed || p.Status.Phase == kube.PodSucceeded {
			terminal++
			continue
		}
		if p.Metadata.DeletionTimestamp != nil {
			if stillTerminating(p.Metadata, now) {
				terminating++
			} else {
				stale++
			}
			continue
		}
		if !p.Metadata.OwnedBy(current) {
			old++
			continue
		}
		if reason := fatalReason(p); reason != "" {
			v.State = StateFailed
			v.Reason = fmt.Sprintf("pod %s: %s", p.Metadata.Name, reason)
			return v
		}
		if p.Status.Phase == kube.PodPending {
			pending++
		}
	}

	switch {
	case terminating > 0:
		v.Reason = fmt.Sprintf("%d pod(s) terminating", terminating)
	case pending > 0:
		v.Reason = fmt.Sprintf("%d pod(s) of current replicaset pending", pending)
	default:
		v.State = StateKick
		v.Reason = "no terminating or pending pods"
	}
	v.Reason += replicaFailure(d) + ignoredSuffix(terminal, old, stale)
	return v
}

func ownedByAny(m kube.ObjectMeta, uids map[string]bool) bool {
	for _, o := range m.OwnerReferences {
		if uids[o.UID] {
			return true
		}
	}
	return false
}

func fatalReason(p kube.Pod) string {
	statuses := slices.Concat(p.Status.InitContainerStatuses, p.Status.ContainerStatuses)
	for _, cs := range statuses {
		if w := cs.State.Waiting; w != nil && fatalWaitingReasons[w.Reason] {
			if w.Message != "" {
				return fmt.Sprintf("container %s %s: %s", cs.Name, w.Reason, w.Message)
			}
			return fmt.Sprintf("container %s %s", cs.Name, w.Reason)
		}
	}
	return ""
}

func replicaFailure(d kube.Deployment) string {
	for _, c := range d.Status.Conditions {
		if c.Type == "ReplicaFailure" && c.Status == "True" {
			return "; ReplicaFailure: " + c.Message
		}
	}
	return ""
}

func stillTerminating(m kube.ObjectMeta, now time.Time) bool {
	var grace time.Duration
	if m.DeletionGracePeriodSeconds != nil {
		grace = time.Duration(*m.DeletionGracePeriodSeconds) * time.Second
	}
	return now.Before(m.DeletionTimestamp.Add(grace))
}

func progressWarning(d kube.Deployment) string {
	for _, c := range d.Status.Conditions {
		if c.Type == "Progressing" && c.Reason == "ProgressDeadlineExceeded" {
			return "ProgressDeadlineExceeded: " + c.Message
		}
	}
	return ""
}

func ignoredSuffix(terminal, old, stale int) string {
	var parts []string
	if stale > 0 {
		parts = append(parts, fmt.Sprintf("%d stuck terminating past grace period", stale))
	}
	if terminal > 0 {
		parts = append(parts, fmt.Sprintf("%d terminal", terminal))
	}
	if old > 0 {
		parts = append(parts, fmt.Sprintf("%d from old replicasets", old))
	}
	if len(parts) == 0 {
		return ""
	}
	return " (ignored pods: " + strings.Join(parts, ", ") + ")"
}
