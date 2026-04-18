package container

import (
	"fmt"
)

func Run(image string, command []string) {
	fmt.Println("[Container] Starting...")
	fmt.Println("Image:", image)
	fmt.Println("Command:", command)

}
