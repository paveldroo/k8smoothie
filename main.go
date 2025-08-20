package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/paveldroo/k8smoothie/errors"
	"github.com/paveldroo/k8smoothie/structs"
)

func main() {
	nsFlag := flag.String("namespace", "", "namespace")
	dnFlag := flag.String("deployment", "", "deployment")
	exitFlag := flag.Int("error-exit-code", 1, "error-exit-code")

	flag.Parse()

	exitCode := *exitFlag

	if *nsFlag == "" || *dnFlag == "" {
		fmt.Println("namespace or deployment is empty, usage: `k8smoothie -namespace=<my-namespace> -deployment=<my-deployment>`")
		os.Exit(exitCode)
	}

	namespace := *nsFlag
	deploymentName := *dnFlag

	fmt.Printf("🧋 Starting k8smoothie with envs: namespace=%s, deployment=%s, error-exit-code=%d\n", namespace, deploymentName, exitCode)

	ticker := time.NewTicker(2 * time.Second)

	for {
		<-ticker.C

		deployment, err := deployment(namespace, deploymentName)
		if err != nil {
			fmt.Printf("🙈 get deployment: %s\n", err.Error())
			os.Exit(exitCode)
		}

		currentReplicaSet, err := currentReplicaSet(namespace, deploymentName)
		if err != nil {
			fmt.Printf("🙈 get replicaset: %s\n", err.Error())
			os.Exit(exitCode)
		}

		if currentReplicaSet.Status.AvailableReplicas == deployment.Spec.Replicas {
			fmt.Printf("🎉🎉🎉 %d of %d pods deployed, task successfully finished!\n", currentReplicaSet.Status.AvailableReplicas, deployment.Spec.Replicas)
			break
		}

		pods, err := pods(namespace, deploymentName)
		if err != nil {
			fmt.Printf("🙈 get pod: %s\n", err.Error())
			os.Exit(exitCode)
		}

		terminating := false
		error := false
		for _, p := range pods.Items {
			if p.Metadata.DeletionTimestamp != nil {
				terminating = true
			}

			switch p.Status.Phase {
			case "Running", "Succeeded":
			default:
				error = true
			}
		}

		if error == true {
			fmt.Println("💥 ooops, something wrong with deploy, you should check manually")
			os.Exit(exitCode)
		}

		if terminating == false {
			fmt.Println("🥾 no terminating pods were found, let's kick the deployment a little")
			if err := kickDeploy(namespace, deploymentName); err != nil {
				fmt.Printf("🙈 annotate deployment: %s\n", err.Error())
				os.Exit(exitCode)
			}
		}

		fmt.Printf("⏳ %d of %d pods deployed, task still in progress...\n", currentReplicaSet.Status.AvailableReplicas, deployment.Spec.Replicas)
	}
}

func deployment(ns, dn string) (structs.Deployment, error) {
	deploymentCmd := exec.Command("kubectl", "-n", ns, "get", "deployment", dn, "-o", "json")
	output, err := deploymentCmd.Output()
	if err != nil {
		return structs.Deployment{}, fmt.Errorf("exec command: %w", err)
	}

	deployment := structs.Deployment{}
	if err := json.Unmarshal(output, &deployment); err != nil {
		return structs.Deployment{}, fmt.Errorf("unmarshal deployment: %w", err)
	}

	if deployment.Spec == nil {
		return structs.Deployment{}, errors.ErrNoDeploymentFound
	}

	return deployment, nil
}

func currentReplicaSet(ns, dn string) (structs.ReplicaSet, error) {
	replicaSetCmd := exec.Command("kubectl", "-n", ns, "get", "replicaset", "--sort-by=.metadata.creationTimestamp", "-o", "json", "-l", "app="+dn)
	output, err := replicaSetCmd.Output()
	if err != nil {
		return structs.ReplicaSet{}, fmt.Errorf("exec command: %w", err)
	}

	r := structs.ReplicaSets{}
	if err := json.Unmarshal(output, &r); err != nil {
		return structs.ReplicaSet{}, fmt.Errorf("unmarshal replicasets: %w", err)
	}

	if len(r.Items) == 0 {
		return structs.ReplicaSet{}, errors.ErrNoReplicaSetsFound
	}

	return r.Items[len(r.Items)-1], nil
}

func pods(ns, dn string) (structs.Pods, error) {
	podstCmd := exec.Command("kubectl", "-n", ns, "get", "pod", "-o", "json", "-l", "app="+dn)
	output, err := podstCmd.Output()
	if err != nil {
		return structs.Pods{}, fmt.Errorf("exec command: %w", err)
	}

	p := structs.Pods{}
	if err := json.Unmarshal(output, &p); err != nil {
		return structs.Pods{}, fmt.Errorf("unmarshal pods: %w", err)
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
