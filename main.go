package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"time"
)

func main() {
	namespace := "default"
	deploymentName := "gracefulapp"

	ticker := time.NewTicker(2 * time.Second)

	for {
		<-ticker.C

		deployment, err := deployment(namespace, deploymentName)
		if err != nil {
			log.Fatalf("🙈 get deployment: %s\n", err.Error())
		}

		currentReplicaSet, err := currentReplicaSet(namespace, deploymentName)
		if err != nil {
			log.Fatalf("🙈 get replicaset: %s\n", err.Error())
		}

		if currentReplicaSet.Status.AvailableReplicas == deployment.Spec.Replicas {
			log.Printf("🎉🎉🎉 %d of %d pods deployed, task successfully finished!\n", currentReplicaSet.Status.AvailableReplicas, deployment.Spec.Replicas)
			break
		}

		pods, err := pods(namespace, deploymentName)
		if err != nil {
			log.Fatalf("🙈 get pod: %s\n", err.Error())
		}

		terminating := false
		for _, p := range pods.Items {
			if p.Metadata.DeletionTimestamp != nil {
				terminating = true
			}
		}

		if terminating == false {
			log.Print("🥾 no terminating pods were found, let's kick the deployment a little")
			if err := kickDeploy(namespace, deploymentName); err != nil {
				log.Fatalf("🙈 annotate deployment: %s\n", err.Error())
			}
		}

		log.Printf("⏳ %d of %d pods deployed, task still in progress...\n", currentReplicaSet.Status.AvailableReplicas, deployment.Spec.Replicas)
	}
}

func deployment(ns, dn string) (Deployment, error) {
	deploymentCmd := exec.Command("kubectl", "-n", ns, "get", "deployment", dn, "-o", "json")
	output, err := deploymentCmd.Output()
	if err != nil {
		return Deployment{}, fmt.Errorf("exec command: %w", err)
	}

	deployment := Deployment{}
	if err := json.Unmarshal(output, &deployment); err != nil {
		return Deployment{}, fmt.Errorf("unmarshal deployment: %w", err)
	}

	if deployment.Spec == nil {
		return Deployment{}, ErrNoDeploymentFound
	}

	return deployment, nil
}

func currentReplicaSet(ns, dn string) (ReplicaSet, error) {
	replicaSetCmd := exec.Command("kubectl", "-n", ns, "get", "replicaset", "--sort-by=.metadata.creationTimestamp", "-o", "json", "-l", "app="+dn)
	output, err := replicaSetCmd.Output()
	if err != nil {
		return ReplicaSet{}, fmt.Errorf("exec command: %w", err)
	}

	r := ReplicaSets{}
	if err := json.Unmarshal(output, &r); err != nil {
		return ReplicaSet{}, fmt.Errorf("unmarshal replicasets: %w", err)
	}

	if len(r.Items) == 0 {
		return ReplicaSet{}, ErrNoReplicaSetsFound
	}

	return r.Items[len(r.Items)-1], nil
}

func pods(ns, dn string) (Pods, error) {
	podstCmd := exec.Command("kubectl", "-n", ns, "get", "pod", "-o", "json", "-l", "app="+dn)
	output, err := podstCmd.Output()
	if err != nil {
		return Pods{}, fmt.Errorf("exec command: %w", err)
	}

	p := Pods{}
	if err := json.Unmarshal(output, &p); err != nil {
		return Pods{}, fmt.Errorf("unmarshal pods: %w", err)
	}

	return p, nil
}

func kickDeploy(ns, dn string) error {
	lastActivatedArg := fmt.Sprintf("last-activated=%s", time.Now().Format(time.RFC3339))
	annotateCmd := exec.Command("kubectl", "-n", ns, "annotate", "deployment", dn, lastActivatedArg, "--overwrite")
	if _, err := annotateCmd.Output(); err != nil {
		return fmt.Errorf("exec command: %w", err)
	}

	return nil
}
