// Entry point: parse args → dispatch cobra command → delegate to internal packages.

package main

import (
	"fmt"
	"os"

	"github.com/i-OmSharma/veil/internal/container"
	"github.com/i-OmSharma/veil/internal/image"
	"github.com/i-OmSharma/veil/internal/state"
	"github.com/spf13/cobra"
)

var (
	// rootCmd is the top-level "veil" command; all subcommands attach to it.
	rootCmd = &cobra.Command{
		Use:   "veil",
		Short: "Veil — A minimal container runtime built in Go",
		Long:  `Veil is a daemonless, OCI-compliant container runtime for learning and experimentation.`,
	}

	// runCmd creates and starts a container from an OCI image.
	runCmd = &cobra.Command{
		Use:   "run [flags] <image> <command>",
		Short: "Run a container",
		Args:  cobra.MinimumNArgs(2),
		Run:   runContainer,
	}

	// pullCmd downloads an OCI image and extracts its rootfs locally.
	pullCmd = &cobra.Command{
		Use:   "pull <image>",
		Short: "Pull an OCI image",
		Args:  cobra.ExactArgs(1),
		Run:   pullImage,
	}

	// pushCmd packs a local directory into an OCI image and uploads it.
	pushCmd = &cobra.Command{
		Use:   "push <source-dir> <image-ref>",
		Short: "Push a directory as an OCI image",
		Args:  cobra.ExactArgs(2),
		Run:   pushImage,
	}

	// psCmd lists all containers tracked in the veil state file.
	psCmd = &cobra.Command{
		Use:   "ps",
		Short: "List containers",
		Run:   listContainers,
	}

	// stopCmd sends SIGTERM to a running container by its ID.
	stopCmd = &cobra.Command{
		Use:   "stop <id>",
		Short: "Stop a container",
		Args:  cobra.ExactArgs(1),
		Run:   stopContainer,
	}

	// imagesCmd lists all OCI images cached locally by veil.
	imagesCmd = &cobra.Command{
		Use:   "images",
		Short: "List local images",
		Run:   listImages,
	}

	// Flags for the run command — zero means "no limit applied".
	memoryLimit int64
	cpuQuota    int64
	cpuPeriod   int64 = 100000 // default 100ms period
	disableNet  bool           // --no-net: skip veth/bridge setup, share host network stack
)

func init() {
	// Wire subcommands into the root command.
	rootCmd.AddCommand(runCmd, pullCmd, pushCmd, psCmd, stopCmd, imagesCmd)

	// Resource limit flags for "veil run".
	runCmd.Flags().Int64VarP(&memoryLimit, "memory", "m", 0, "Memory limit in bytes")
	runCmd.Flags().Int64Var(&cpuQuota, "cpu-quota", 0, "CPU quota in microseconds")
	runCmd.Flags().Int64Var(&cpuPeriod, "cpu-period", 100000, "CPU period in microseconds")
	// Networking flag: default ON (isolated veth), --no-net reverts to host network.
	runCmd.Flags().BoolVar(&disableNet, "no-net", false, "Disable container networking (share host network stack)")
}

// runContainer builds a ResourceConfig from flags and delegates to container.Run().
// Run() = create namespaces + apply cgroups + setup network + exec child process.
func runContainer(cmd *cobra.Command, args []string) {
	imageRef := args[0]
	command := args[1:]

	resources := &container.ResourceConfig{
		MemoryMax: memoryLimit,
		CPUQuota:  cpuQuota,
		CPUPeriod: cpuPeriod,
		// Network is enabled by default — pass --no-net to share host network stack.
		Network: !disableNet,
	}

	container.Run(imageRef, command, resources)
}

// pullImage downloads the OCI image and prints the local rootfs path on success.
func pullImage(cmd *cobra.Command, args []string) {
	rootfs, err := image.Pull(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "[veil] pull error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[veil] Pulled to: %s\n", rootfs)
}

// pushImage packs args[0] directory into an OCI image at args[1] reference.
func pushImage(cmd *cobra.Command, args []string) {
	if err := image.Push(args[0], args[1]); err != nil {
		fmt.Fprintf(os.Stderr, "[veil] push error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[veil] Pushed: %s\n", args[1])
}

// shortID returns up to 8 chars of an ID safely — PID-based IDs can be shorter.
func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// listContainers reads state from disk and prints a formatted table.
func listContainers(cmd *cobra.Command, args []string) {
	s, err := state.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[veil] state error: %v\n", err)
		os.Exit(1)
	}
	containers := s.List()
	if len(containers) == 0 {
		fmt.Println("No containers")
		return
	}
	fmt.Printf("%-12s %-20s %-10s %-20s\n", "ID", "IMAGE", "STATUS", "COMMAND")
	for _, c := range containers {
		fmt.Printf("%-12s %-20s %-10s %-20s\n",
			shortID(c.ID), c.Image, c.Status, fmt.Sprintf("%v", c.Command))
	}
}

// stopContainer delegates to state.Stop() which sends SIGTERM and updates status.
func stopContainer(cmd *cobra.Command, args []string) {
	id := args[0]
	if err := state.Stop(id); err != nil {
		fmt.Fprintf(os.Stderr, "[veil] stop error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[veil] Stopped: %s\n", id)
}

// listImages prints all OCI images cached locally under veil's image store.
func listImages(cmd *cobra.Command, args []string) {
	images, err := image.ListLocalImages()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[veil] list error: %v\n", err)
		os.Exit(1)
	}
	if len(images) == 0 {
		fmt.Println("No images")
		return
	}
	fmt.Println("Local images:")
	for _, img := range images {
		fmt.Println(" -", img)
	}
}

func main() {
	// Re-exec child path: container.Run() spawns "/proc/self/exe child ..."
	// Must intercept before cobra sees the args to avoid command-not-found errors.
	if len(os.Args) > 1 && os.Args[1] == "child" {
		container.Child()
		return
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
