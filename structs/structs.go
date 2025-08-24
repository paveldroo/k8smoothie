package structs

import "time"

type AppLabel struct {
	AppName string `json:"app"`
}

type Metadata struct {
	Labels AppLabel `json:"labels"`
}

type Template struct {
	Metadata Metadata `json:"metadata"`
}

type Spec struct {
	Replicas int      `json:"replicas"`
	Template Template `json:"template"`
}

type Deployment struct {
	Spec *Spec `json:"spec"`
}

type Status struct {
	AvailableReplicas int   `json:"availableReplicas"`
	Conditions        []any `json:"conditions"`
}

type ReplicaSet struct {
	Status *Status `json:"status"`
}

type ReplicaSets struct {
	Items []ReplicaSet `json:"items"`
}

type PodMetadata struct {
	DeletionTimestamp *time.Time `json:"deletionTimestamp"`
}

type PodStatus struct {
	Phase string `json:"phase"`
}

type Pod struct {
	Metadata PodMetadata `json:"metadata"`
	Status   PodStatus   `json:"status"`
}

type Pods struct {
	Items []Pod `json:"items"`
}
