// 1. We will parse args.
// 2. Validate.
// 3. Container layer call.

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/i-OmSharma/veil/internal/container"
	"github.com/i-OmSharma/veil/internal/image" // FIXED: spelling of Sharma and internal
)

func main() {

	// avoid duplicate log in child process
	if len(os.Args) > 1 && os.Args[1] != "child" {
		fmt.Println("[veil] Starting...")
	}

	// Validate, Identify command
	if len(os.Args) < 2 {
		fmt.Println("Usage: veil <command> [args]")
		os.Exit(1)
	}

	// identify command
	switch os.Args[1] {

	// Run() initializes and executes a container by setting up isolation and spawning the process.
	case "run":

		// Create a new FlagSet for the 'run' command to avoid conflicts
		runCmd := flag.NewFlagSet("run", flag.ExitOnError)

		// Define dynamic resource flags
		memLimit := runCmd.Int64("memory", 0, "Memory limit in bytes")
		cpuQuota := runCmd.Int64("cpu-quota", 0, "CPU quota in microseconds")
		cpuPeriod := runCmd.Int64("cpu-period", 100000, "CPU period in microseconds (default 100ms)")

		// Parse flags from args after "run"
		runCmd.Parse(os.Args[2:])

		// Remaining args are image and command
		args := runCmd.Args()
		if len(args) < 2 {
			fmt.Println("Usage: veil run [options] <image> <command>")
			os.Exit(1)
		}

		// Changed variable name to imageRef to avoid conflict with the 'image' package
		imageRef := args[0]
		command := args[1:]

		// Create Resource Config
		resources := &container.ResourceConfig{
			MemoryMax: *memLimit,
			CPUQuota:  *cpuQuota,
			CPUPeriod: *cpuPeriod,
		}

		/*
			Run():
			  → command prepare
			  → namespace apply
			  → process start

			“Run = create + configure + start container process”
		*/
		// Pass config to run - Container execution lifecycle entry point.
		container.Run(imageRef, command, resources)

	// child for isolation.
	case "child":
		container.Child()

	// (Pull) command: Downloads an OCI image and extracts rootfs
	case "pull":
		if len(os.Args) < 3 {
			fmt.Println("Usage: veil pull <image>")
			os.Exit(1)
		}
		imageRef := os.Args[2]
		rootfs, err := image.Pull(imageRef)
		if err != nil {
			fmt.Printf("[veil] pull error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[veil] Pulled to: %s\n", rootfs)

	// (Push) command: Packs a directory into an OCI image and uploads
	case "push":
		if len(os.Args) < 4 {
			fmt.Println("Usage: veil push <source-dir> <image-ref>")
			os.Exit(1)
		}
		sourceDir := os.Args[2]
		imageRef := os.Args[3]
		if err := image.Push(sourceDir, imageRef); err != nil {
			fmt.Printf("[veil] push error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[veil] Pushed: %s\n", imageRef)

	// (Images) command: Lists locally available images
	case "images":
		images, err := image.ListLocalImages()
		if err != nil {
			fmt.Printf("[veil] list error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Local images:")
		for _, img := range images {
			fmt.Println(" -", img)
		}

	// Unimplemented commands grouped together for cleaner code
	case "stop", "ps", "delete":
		fmt.Println("Not implemented yet")

	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
	}
}