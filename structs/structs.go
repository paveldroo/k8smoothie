package structs

import "time"

type Metadata struct {
	Name string `json:"name"`
}

type Spec struct {
	Replicas int `json:"replicas"`
}

type Deployment struct {
	Metadata Metadata `json:"metadata"`
	Spec     *Spec    `json:"spec"`
}

type Status struct {
	AvailableReplicas int   `json:"availableReplicas"`
	Conditions        []any `json:"conditions"`
}

type ReplicaSet struct {
	Metadata Metadata `json:"metadata"`
	Status   *Status  `json:"status"`
}

type ReplicaSets struct {
	Items []ReplicaSet `json:"items"`
}

type PodMetadata struct {
	DeletionTimestamp *time.Time `json:"deletionTimestamp"`
	Name              string     `json:"name"`
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
