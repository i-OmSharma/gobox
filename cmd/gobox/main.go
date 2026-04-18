// 1. We will parse args.
// 2. Validate.
// 3. Container layer call.

package main

import (
	"fmt"
	"os"
	"github.com/i-OmSharma/gobox/internal/container"
)

func main() {
	fmt.Println("Start gobox")

	//check minimum arguments
	if len(os.Args) < 4 {
		fmt.Println("Usage: gobox run <image> <commad>")
		os.Exit(1)
	}
	//identify command(run)
	switch os.Args[1] {
	case "run":
		image := os.Args[2]
		cmd := os.Args[3:] // if we have multiple args

		container.Run(image, cmd)
	// (Stop) command
	case "stop":

	//(ps) command
	case "ps":

	default:
		fmt.Print(("Unknown command"))
	}
}
