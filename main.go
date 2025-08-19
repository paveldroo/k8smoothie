package main

import (
	"encoding/json"
	"log"
	"os/exec"
	"time"
)

type Spec struct {
	Replicas int `json:"replicas"`
}

type Deployment struct {
	Spec   Spec   `json:"spec"`
	Status Status `json:"status"`
}

type Status struct {
	AvailableReplicas int `json:"availableReplicas"`
}

type ReplicaSet struct {
	Status Status
}

type ReplicaSets struct {
	Items []ReplicaSet
}

func main() {
	// lastActivatedArg := fmt.Sprintf(format string, a ...any)
	// cmd := exec.Command("kubectl", "annotate", "deployment", "gracefulapp", "last-activated=2025-08-18T19:35:13Z", "--overwrite")

	ticker := time.NewTicker(1 * time.Second)

	for {
		<-ticker.C

		deploymentCmd := exec.Command("kubectl", "get", "deployment", "gracefulapp", "-o", "json")
		output, err := deploymentCmd.Output()
		if err != nil {
			log.Fatalf("🙈 run command error: %s\n", err.Error())
		}

		deployment := Deployment{}
		if err := json.Unmarshal(output, &deployment); err != nil {
			log.Fatalf("🙈 unmarshal deployments: %s\n", err.Error())
		}

		replicaSetCmd := exec.Command("kubectl", "get", "replicasets", "--sort-by=.metadata.creationTimestamp", "-o", "json", "-l", "app=gracefulapp")
		output, err = replicaSetCmd.Output()
		if err != nil {
			log.Fatalf("🙈 run command error: %s\n", err.Error())
		}

		r := ReplicaSets{}
		if err := json.Unmarshal(output, &r); err != nil {
			log.Fatalf("🙈 unmarshal replicasets: %s\n", err.Error())
		}

		if len(r.Items) == 0 {
			log.Fatalf("🙈 no replicasets were found, it's impossible to manage deploy\n")
		}

		currentReplicaSet := r.Items[len(r.Items)-1]

		if currentReplicaSet.Status.AvailableReplicas == deployment.Spec.Replicas {
			log.Printf("🎉🎉🎉 %d of %d pods deployed, task successfully finished!\n", currentReplicaSet.Status.AvailableReplicas, deployment.Spec.Replicas)
			break
		}

		log.Printf("⏳ %d of %d pods deployed, task still in progress...\n", currentReplicaSet.Status.AvailableReplicas, deployment.Spec.Replicas)
	}
}
