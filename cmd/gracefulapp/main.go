package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	fmt.Println("Starting app...")
	shutdown := make(chan struct{})
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-sigChan
		fmt.Println("Graceful in 240 seconds countdown started...")
		go func() {
			timeleft := 240

			ticker := time.NewTicker(5 * time.Second)
			for range ticker.C {
				timeleft -= 5
				fmt.Printf("%d left until exit...\n", timeleft)
			}
		}()
		time.AfterFunc(4*time.Minute, func() {
			fmt.Println("Graceful finished, exiting, bye!")
			os.Exit(1)
		})
	}()

	<-shutdown
}
