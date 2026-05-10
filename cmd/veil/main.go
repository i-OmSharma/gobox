// 1. We will parse args.
// 2. Validate.
// 3. Container layer call.

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/i-OmSharma/veil/internal/container"
)

func main() {

	// avoid duplicate log in child process
	if len(os.Args) > 1 && os.Args[1] != "child" {
		fmt.Println("[veil] Starting...")
	}

	// Validate, Identfy command
	if len(os.Args) < 2 {
		fmt.Println("Usage: veil <command> [args]")
		os.Exit(1)
	}

	//identify command
	switch os.Args[1] {

		//Run() initializes and executes a container by setting up isolation and spawning the process.
	case "run":

		//Create a new FlagSet for the 'run' command to avoid conflicts
		runCmd := flag.NewFlagSet("run", flag.ExitOnError)

		//Define dynamic resource flags
		memLimit := runCmd.Int64("memory", 0, "Memory limit in bytes")
		cpuQuota := runCmd.Int64("cpu-quota", 0, "CPU qupta in microseconds")
		cpuPeriod := runCmd.Int64("cpu-period", 100000, "CPU period in microsecond (default 100ms)")

		// Parse flags from args after "run"
		runCmd.Parse(os.Args[2:])

		// Remaining args are image and command
		args := runCmd.Args()
		if len(args) < 2 {
			fmt.Println("Usage: veil run [options] <image> <command>")
			os.Exit(1)
		}

		image := args[0]
		command := args[1:]


		// Cerate Resource Config
		resources := &container.ResourceConfig{
			MemoryMax: *memLimit,
			CPUQuota: *cpuQuota,
			CPUPeriod: *cpuPeriod,
		}

		// Pass confg to run 
		container.Run(image, command, resources)

		if len(os.Args) < 4 {
			fmt.Println("Usage: veil run <image> <commad>")
			os.Exit(1)
		}

		// image := os.Args[2]
		// cmd := os.Args[3:] // if we have multiple args

		// container.Run(image, cmd) // Container execution lifecycle entry point. we are doing (create+start) in single step.

		/*
			Run():
			  → command prepare
			  → namespace apply
			  → process start

			“Run = create + configure + start container process”
		*/

	//child for isolation.
	case"child":
		container.Child()


	// (Stop) command
	case "stop":
		fmt.Println("Not implemented yet")

	//(ps) command
	case "ps":
		fmt.Println("Not implemented yet")

	case "delete":
		fmt.Println("Not implemented yet")

	default:
		fmt.Print("Unknown command", os.Args[1])
	}
}
