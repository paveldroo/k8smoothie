package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/paveldroo/k8smoothie/errors"
	"github.com/paveldroo/k8smoothie/structs"
)

func main() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("💥 PANIC: %v\n", r)
			debug.PrintStack()
			os.Exit(0)
		}
	}()

	nsFlag := flag.String("namespace", "", "namespace")
	dnFlag := flag.String("deployment", "", "deployment")
	delayFlag := flag.Int("frequaency", 3, "frequency")
	exitFlag := flag.Int("error-exit-code", 1, "error-exit-code")

	flag.Parse()

	delay := *delayFlag
	exitCode := *exitFlag

	if *nsFlag == "" || *dnFlag == "" {
		fmt.Println("namespace or deployment is empty, usage: `k8smoothie -namespace=<my-namespace> -deployment=<my-deployment>`")
		os.Exit(exitCode)
	}

	namespace := *nsFlag
	deploymentName := *dnFlag

	fmt.Printf("🧋 Starting k8smoothie with args: namespace=%s, deployment=%s, delay=%d, error-exit-code=%d\n", namespace, deploymentName, delay, exitCode)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		sig := <-sigChan
		fmt.Printf("🛑 Received signal: %v, exiting...\n", sig)
		os.Exit(exitCode)
	}()

	ticker := time.NewTicker(time.Duration(delay) * time.Second)

	for {
		fmt.Println("🔄🔄🔄🔄 Starting new iteration...")
		<-ticker.C
		fmt.Println("⏰ Ticker fired, checking deployment status...")

		deploy, err := deployment(namespace, deploymentName)
		if err != nil {
			fmt.Printf("🙈 get deployment: %s\n", err.Error())
			os.Exit(exitCode)
		}
		fmt.Printf("📊 Deployment replicas: desired=%d\n", deploy.Spec.Replicas)

		currentReplicaSet, err := currentReplicaSet(namespace, deploymentName)
		if err != nil {
			fmt.Printf("🙈 get replicaset: %s\n", err.Error())
			os.Exit(exitCode)
		}
		fmt.Printf("📊 Current replicaset: available=%d\n", currentReplicaSet.Status.AvailableReplicas)

		if currentReplicaSet.Status.AvailableReplicas == deploy.Spec.Replicas {
			fmt.Printf("🎉🎉🎉 %d of %d pods deployed, task successfully finished!\n", currentReplicaSet.Status.AvailableReplicas, deploy.Spec.Replicas)
			break
		}

		pods, err := pods(namespace, deploymentName)
		if err != nil {
			fmt.Printf("🙈 get pod: %s\n", err.Error())
			os.Exit(exitCode)
		}
		fmt.Printf("📊 Pods: count=%d\n", len(pods.Items))

		terminating := false
		error := false
		pending := false
		for _, p := range pods.Items {
			if p.Metadata.DeletionTimestamp != nil {
				terminating = true
			}

			switch p.Status.Phase {
			case "Running", "Succeeded":
			case "Pending":
				pending = true
			default:
				error = true
			}
		}

		if error == true {
			fmt.Println("💥 ooops, something wrong with deploy, you should check manually")
			os.Exit(exitCode)
		}

		if pending == true {
			fmt.Println("🤔 one of the pods in Pending status, if it takes too long - you'd better check it out")
			continue
		}

		if terminating == false {
			fmt.Println("🥾 no terminating pods were found, let's kick the deployment a little")
			if err := kickDeploy(namespace, deploymentName); err != nil {
				fmt.Printf("🙈 annotate deployment: %s\n", err.Error())
				os.Exit(exitCode)
			}
		}

		fmt.Printf("⏳ %d of %d pods deployed, task still in progress...\n", currentReplicaSet.Status.AvailableReplicas, deploy.Spec.Replicas)
		fmt.Println("✅✅✅✅ Finished iteration, we're going to the next round...")
		os.Stdout.Sync()
	}
}

func deployment(ns string, dn string) (structs.Deployment, error) {
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

func currentReplicaSet(ns, deployName string) (structs.ReplicaSet, error) {
	replicaSetCmd := exec.Command("kubectl", "-n", ns, "get", "replicaset", "-o", "json")
	output, err := replicaSetCmd.Output()
	if err != nil {
		return structs.ReplicaSet{}, fmt.Errorf("exec command: %w", err)
	}

	r := structs.ReplicaSets{}
	if err := json.Unmarshal(output, &r); err != nil {
		return structs.ReplicaSet{}, fmt.Errorf("unmarshal replicasets: %w", err)
	}

	// filter by current deployment name
	items := make([]structs.ReplicaSet, 0, len(r.Items))
	for _, rs := range r.Items {
		if strings.HasPrefix(rs.Metadata.Name, deployName) {
			items = append(items, rs)
		}
	}

	r.Items = items

	if len(r.Items) == 0 {
		return structs.ReplicaSet{}, errors.ErrNoReplicaSetsFound
	}

	// find first resplicaset in progressing status
	for _, rs := range r.Items {
		if len(rs.Status.Conditions) != 0 {
			return rs, nil
		}
	}

	// if ew can't find active replicaset it means we're done, find replicaset with max replicas
	maxReplicaSet := r.Items[0]
	for _, rs := range r.Items {
		if rs.Status.AvailableReplicas > maxReplicaSet.Status.AvailableReplicas {
			maxReplicaSet = rs
		}
	}

	return maxReplicaSet, nil
}

func pods(ns, deployName string) (structs.Pods, error) {
	podstCmd := exec.Command("kubectl", "-n", ns, "get", "pod", "-o", "json")
	output, err := podstCmd.Output()
	if err != nil {
		return structs.Pods{}, fmt.Errorf("exec command: %w", err)
	}

	p := structs.Pods{}
	if err := json.Unmarshal(output, &p); err != nil {
		return structs.Pods{}, fmt.Errorf("unmarshal pods: %w", err)
	}

	// filter by current deployment name
	items := make([]structs.Pod, 0, len(p.Items))
	for _, pod := range p.Items {
		if strings.HasPrefix(pod.Metadata.Name, deployName) {
			items = append(items, pod)
		}
	}

	p.Items = items

	if len(p.Items) == 0 {
		return structs.Pods{}, errors.ErrNoPodsFound
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
