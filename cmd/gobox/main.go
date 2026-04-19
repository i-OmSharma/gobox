// 1. We will parse args.
// 2. Validate.
// 3. Container layer call.

package main

import (
	"fmt"
	"github.com/i-OmSharma/gobox/internal/container"
	"os"
)

func main() {

	// avoid duplicate log in child process
	if len(os.Args) > 1 && os.Args[1] != "child" {
		fmt.Println("[gobox] Starting...")
	}

	// Validate 
	if len(os.Args) < 2 {
		fmt.Println("Usage: gobox <command> [args]")
		os.Exit(1)
	}

	//identify command
	switch os.Args[1] {

		//Run() initializes and executes a container by setting up isolation and spawning the process.
	case "run":
		if len(os.Args) < 4 {
			fmt.Println("Usage: gobox run <image> <commad>")
			os.Exit(1)
		}

		image := os.Args[2]
		cmd := os.Args[3:] // if we have multiple args

		container.Run(image, cmd) // Container execution lifecycle entry point. we are doing (create+start) in single step.

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
