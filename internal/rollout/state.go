package rollout

import (
	"fmt"
	"slices"
	"strings"

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
	Key     string
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
// It kicks whenever the current ReplicaSet has fewer live pods than it wants.
func Evaluate(d kube.Deployment, rsList []kube.ReplicaSet, pods []kube.Pod) Verdict {
	v := Verdict{Warning: progressWarning(d)}
	st := d.Status

	if st.ObservedGeneration < d.Metadata.Generation {
		v.Reason = fmt.Sprintf("waiting for controller to observe generation %d (observed %d)", d.Metadata.Generation, st.ObservedGeneration)
		return v
	}

	if st.UpdatedReplicas == d.DesiredReplicas() && st.Replicas == st.UpdatedReplicas && st.AvailableReplicas == st.UpdatedReplicas {
		v.State = StateDone
		v.Reason = fmt.Sprintf("%d of %d pods updated and available", st.AvailableReplicas, d.DesiredReplicas())
		return v
	}

	if d.Spec.Paused {
		v.State = StateFailed
		v.Reason = "deployment is paused"
		v.Key = "paused"
		return v
	}

	revision := d.Metadata.Annotations[kube.AnnotationRevision]
	owned := map[string]bool{}
	current := ""
	var currentDesired int32
	for _, rs := range rsList {
		if !rs.Metadata.OwnedBy(d.Metadata.UID) {
			continue
		}
		owned[rs.Metadata.UID] = true
		if revision != "" && rs.Metadata.Annotations[kube.AnnotationRevision] == revision {
			current = rs.Metadata.UID
			currentDesired = rs.DesiredReplicas()
		}
	}

	var live int32
	for _, p := range pods {
		if !ownedByAny(p.Metadata, owned) {
			continue
		}
		if p.Status.Phase == kube.PodFailed || p.Status.Phase == kube.PodSucceeded {
			continue
		}
		if p.Metadata.DeletionTimestamp != nil || !p.Metadata.OwnedBy(current) {
			continue
		}
		if key, reason := fatalReason(p); key != "" {
			v.State = StateFailed
			v.Key = p.Metadata.Name + "/" + key
			v.Reason = fmt.Sprintf("pod %s: %s", p.Metadata.Name, reason)
			return v
		}
		live++
	}

	switch {
	case current == "":
		v.State = StateKick
		v.Reason = "current replicaset not found"
	case live < currentDesired:
		v.State = StateKick
	}
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

func fatalReason(p kube.Pod) (key, reason string) {
	statuses := slices.Concat(p.Status.InitContainerStatuses, p.Status.ContainerStatuses)
	for _, cs := range statuses {
		if w := cs.State.Waiting; w != nil && fatalWaitingReasons[w.Reason] {
			key = cs.Name + "/" + w.Reason
			if w.Message != "" {
				return key, fmt.Sprintf("container %s %s: %s", cs.Name, w.Reason, oneLine(w.Message))
			}
			return key, fmt.Sprintf("container %s %s", cs.Name, w.Reason)
		}
	}
	return "", ""
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func progressWarning(d kube.Deployment) string {
	for _, c := range d.Status.Conditions {
		if c.Type == "Progressing" && c.Reason == "ProgressDeadlineExceeded" {
			return "ProgressDeadlineExceeded: " + oneLine(c.Message)
		}
	}
	return ""
}
