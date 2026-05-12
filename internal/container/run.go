// Package container orchestrates the full container lifecycle.
//
// Execution sequence in Run():
//
//	resolve image → mount overlay → register state → clone namespaces
//	  → SIGSTOP → apply cgroups → setup veth → SIGCONT → wait → cleanup
//
// The SIGSTOP window is critical: it gives the parent a race-free moment to
// apply cgroups and network config before the child process runs any code.
// Real runtimes (runc, containerd) use the same pattern.
package container

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/google/uuid"
	"github.com/i-OmSharma/veil/internal/cgroup"
	img "github.com/i-OmSharma/veil/internal/image" // aliased to avoid shadowing the 'image' arg in Run()
	"github.com/i-OmSharma/veil/internal/network"
	"github.com/i-OmSharma/veil/internal/overlayfs"
	"github.com/i-OmSharma/veil/internal/state"
)

// ResourceConfig carries runtime constraints passed in from the CLI.
// Zero values mean "no limit" for numeric fields.
type ResourceConfig struct {
	MemoryMax int64 // Hard RSS limit in bytes (0 = unlimited)
	CPUQuota  int64 // CPU time per period in microseconds (0 = unlimited)
	CPUPeriod int64 // Scheduling window in microseconds (default 100ms = 100000)
	Network   bool  // true = isolated netns + veth pair; false = share host network (--no-net)
}

// Run is the top-level entry point for container execution.
// It wires all subsystems together and blocks until the container exits.
func Run(image string, command []string, resources *ResourceConfig) {
	fmt.Printf("[container] starting  image=%s command=%v memory=%d cpu=%d/%d net=%v\n",
		image, command, resources.MemoryMax, resources.CPUQuota, resources.CPUPeriod, resources.Network)

	if len(command) == 0 {
		fmt.Fprintln(os.Stderr, "[container] no command provided")
		os.Exit(1)
	}

	// Step 1: resolve image to a local rootfs path, pulling from registry if needed.
	rootfs, err := img.Resolve(image)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[container] image error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[container] rootfs: %s\n", rootfs)

	// Step 2: mount overlayfs — container writes land in the upper dir,
	// leaving the image's lowerdir completely untouched.
	// UUID (first 12 chars) gives each container a short, unique ID.
	containerID := uuid.New().String()[:12]
	ovl := overlayfs.New(containerID, rootfs)
	mergedDir, err := ovl.Mount()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[container] overlay mount: %v\n", err)
		os.Exit(1)
	}

	// Step 3: register in state with PID=0 before Start() so `veil ps` shows
	// the container immediately. PID is patched to the real value after Start().
	record := state.NewContainer(containerID, image, mergedDir, command, 0)
	if s, err := state.Load(); err == nil {
		_ = s.Add(record)
	}

	// Step 4: build the namespace clone flags.
	// CLONE_NEWPID — container's init becomes PID 1 in its own PID space
	// CLONE_NEWUTS — isolated hostname (set to veil-<pid> in Child)
	// CLONE_NEWNS  — isolated mount table, required for pivot_root
	// CLONE_NEWIPC — isolated IPC objects (message queues, semaphores)
	// CLONE_NEWNET — (conditional) empty network stack; veth attached post-Start
	cloneFlags := uintptr(
		syscall.CLONE_NEWPID |
			syscall.CLONE_NEWUTS |
			syscall.CLONE_NEWNS |
			syscall.CLONE_NEWIPC,
	)
	if resources.Network {
		cloneFlags |= syscall.CLONE_NEWNET
	}

	// Step 5: ensure the host bridge exists before spawning the child.
	// SetupBridge is idempotent — returns immediately if veil0 already exists.
	if resources.Network {
		if err := network.SetupBridge(); err != nil {
			fmt.Printf("[container] bridge warning: %v (continuing without networking)\n", err)
			resources.Network = false
		}
	}

	// Re-exec this binary with "child" as the first argument so Child() takes
	// over inside the new namespaces and sets up the container environment.
	cmd := exec.Command("/proc/self/exe", append([]string{"child", image}, command...)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = []string{
		"VEIL_CHILD=1",
		fmt.Sprintf("VEIL_ROOTFS=%s", mergedDir),
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: cloneFlags}

	// Step 6: Start() clones the namespaces and forks the child process.
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "[container] start: %v\n", err)
		ovl.Unmount()
		os.Exit(1)
	}

	// Patch the real PID into state now that the process exists.
	if s, err := state.Load(); err == nil {
		if c, ok := s.Containers[containerID]; ok {
			c.PID = cmd.Process.Pid
			_ = s.Save()
		}
	}

	// Step 7: SIGSTOP — freeze the child before it executes any code.
	// This opens a safe window to configure cgroups and network without races.
	pid := cmd.Process.Pid
	if err := cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		fmt.Fprintf(os.Stderr, "[container] sigstop: %v\n", err)
		cmd.Process.Kill()
		ovl.Unmount()
		os.Exit(1)
	}

	// Step 8: apply cgroups while the process is frozen.
	// Only write limits that were actually set — zero means "no limit".
	cgConfig := &cgroup.CgroupConfig{
		ID:        fmt.Sprintf("veil-%d", pid),
		MemoryMax: resources.MemoryMax,
		CPUQuota:  resources.CPUQuota,
		CPUPeriod: resources.CPUPeriod,
	}
	if cgConfig.MemoryMax > 0 || cgConfig.CPUQuota > 0 {
		if err := cgConfig.Apply(pid); err != nil {
			fmt.Fprintf(os.Stderr, "[container] cgroup: %v\n", err)
			ovl.Unmount()
			cmd.Process.Kill()
			os.Exit(1)
		}
		fmt.Println("[container] cgroups applied")
	}

	// Step 9: attach the veth pair while the process is still frozen.
	// We need the PID to name the host-side veth and to enter the container's
	// netns via nsenter (/proc/<pid>/ns/net).
	if resources.Network {
		if err := network.SetupVeth(pid, network.GetContainerIP()); err != nil {
			fmt.Printf("[container] veth warning: %v (continuing without networking)\n", err)
		}
	}

	// Step 10: SIGCONT — resume with limits and network already in place.
	if err := cmd.Process.Signal(syscall.SIGCONT); err != nil {
		fmt.Fprintf(os.Stderr, "[container] sigcont: %v\n", err)
		cmd.Process.Kill()
		os.Exit(1)
	}

	// Step 11: block until the container process exits.
	if err := cmd.Wait(); err != nil {
		fmt.Printf("[container] exit: %v\n", err)
	}

	// Step 12: cleanup in reverse dependency order.
	// cgroups must be removed before overlay (procs must be gone first).
	if err := cgConfig.Remove(); err != nil {
		fmt.Printf("[container] cgroup cleanup: %v\n", err)
	}
	if resources.Network {
		// Deleting the host-side veth automatically removes the container side too.
		if err := network.CleanupVeth(pid); err != nil {
			fmt.Printf("[container] veth cleanup: %v\n", err)
		}
	}
	if err := ovl.Unmount(); err != nil {
		fmt.Printf("[container] overlay cleanup: %v\n", err)
	}
	if s, err := state.Load(); err == nil {
		if c, ok := s.Containers[containerID]; ok {
			c.Status = "exited"
			_ = s.Save()
		}
	}

	fmt.Println("[container] finished")
}
