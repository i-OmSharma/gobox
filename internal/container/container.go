//Orchestration Layer

package container

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/i-OmSharma/veil/internal/cgroup"
	img "github.com/i-OmSharma/veil/internal/image"     // Aliased to 'img' to prevent shadowing the 'image' variable in Run()
	"github.com/i-OmSharma/veil/internal/overlayfs" // Added OverlayFS handling
)

// ResourceConfig holds dynamic resource limits
type ResourceConfig struct {
	MemoryMax int64 // Bytes
	CPUQuota  int64 // Microseconds
	CPUPeriod int64 // Microseconds
}

// “Run = create + configure + start container process”
func Run(image string, command []string, resources *ResourceConfig) {
	fmt.Println("[container] Starting...")
	fmt.Println("Image:", image)
	fmt.Println("Command:", command)
	fmt.Printf("[container] Resources: Memory=%d, CPU=%d/%d\n", resources.MemoryMax, resources.CPUQuota, resources.CPUPeriod)

	// Safyty Check
	if len(command) == 0 {
		fmt.Fprintln(os.Stderr, "[container] No command provided")
		os.Exit(1)
	}

	// Resolve/Pull image to get rootfs path
	// Using the imported 'img' package to resolve the image name into a local root filesystem path
	rootfs, err := img.Resolve(image)
	if err != nil {
		fmt.Printf("[container] image error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[container] Using rootfs: %s\n", rootfs)

	// Mount overlay BEFORE starting child — env must be fully set before Start()
	// Use parent PID as container ID (unique per invocation, known before child starts)
	containerID := fmt.Sprintf("%d", os.Getpid())
	ovl := overlayfs.New(containerID, rootfs)
	mergedDir, err := ovl.Mount()
	if err != nil {
		fmt.Printf("[container] overlay mount error: %v\n", err)
		os.Exit(1)
	}

	cmd := exec.Command("/proc/self/exe", append([]string{"child", image}, command...)...)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = []string{
		"VEIL_CHILD=1",
		fmt.Sprintf("VEIL_ROOTFS=%s", mergedDir),
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWPID |
			syscall.CLONE_NEWUTS |
			syscall.CLONE_NEWNS |
			syscall.CLONE_NEWIPC,
	}

	if err := cmd.Start(); err != nil {
		fmt.Printf("[container] start error: %v\n", err)
		ovl.Unmount()
		os.Exit(1)
	}

	// Freeze child immediately — apply cgroup before any execution
	if err := cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		fmt.Printf("[container] failed to stop process: %v\n", err)
		cmd.Process.Kill()
		ovl.Unmount()
		os.Exit(1)
	}

	// 3. Apply Dynamic Cgroup WHILE Paused
	pid := cmd.Process.Pid
	cgConfig := &cgroup.CgroupConfig{
		ID:        fmt.Sprintf("veil-%d", pid),
		MemoryMax: resources.MemoryMax, // DYNAMIC VALUE
		CPUQuota:  resources.CPUQuota,  // DYNAMIC VALUE
		CPUPeriod: resources.CPUPeriod, // DYNAMIC VALUE
	}

	//Apply cGroup before resuming
	// Only apply if limits are actually set
	if cgConfig.MemoryMax > 0 || cgConfig.CPUQuota > 0 {
		if err := cgConfig.Apply(pid); err != nil {
			fmt.Printf("[container] cgroup apply error: %v\n", err)
			// Cleanup overlay on error to prevent resource leaks before killing process
			ovl.Unmount()
			cmd.Process.Kill()
			os.Exit(1)
		}
		fmt.Println("[container] Cgroup applied successfully")
	}

	// 4. Resume process
	// Now the process runs with limits enforced
	if err := cmd.Process.Signal(syscall.SIGCONT); err != nil {
		fmt.Printf("[container] failed to continue process: %v\n", err)
		cmd.Process.Kill()
		os.Exit(1)
	}

	// 5. Wait for Completion to exit
	if err := cmd.Wait(); err != nil {
		fmt.Printf("[container] exit error: %v\n", err)
	}

	// 6. Cleanup Cgroups + OverlayFS
	if err := cgConfig.Remove(); err != nil {
		fmt.Printf("[container] cleanup warning: %v\n", err)
	}
	
	// Unmount and clean up the overlay filesystem when container finishes
	if err := ovl.Unmount(); err != nil {
		fmt.Printf("[container] overlay cleanup warning: %v\n", err)
	}

	fmt.Println("[container] Finished")
}
//

func Child() {

	fmt.Println("[init] Inside container init")

	// validate args
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "[init] Invalid child invocation")
		os.Exit(1)
	}

	// Get rootfs from environment
	rootfs := os.Getenv("VEIL_ROOTFS")
	if rootfs == "" {
		fmt.Fprintln(os.Stderr, "[init] VEIL_ROOTFS not set")
		os.Exit(1)
	}

	// 1. Isolate mount namespace

	if err := syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""); err != nil {
		fmt.Fprintf(os.Stderr, "[init] mount propagation error: %v\n", err)
		os.Exit(1)
	}

	//2. Set hostname

	hostName := fmt.Sprintf("veil-%d", os.Getpid())
	if err := syscall.Sethostname([]byte(hostName)); err != nil {
		fmt.Fprintf(os.Stderr, "[init] HostName error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[init] Hostname set to:", hostName)

	// Pivot to container rootfs
	// This physically swaps the root filesystem and MUST happen BEFORE mounting /proc
	if err := overlayfs.PivotRoot(rootfs); err != nil {
		fmt.Fprintf(os.Stderr, "[init] pivot_root error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[init] Pivoted to rootfs")

	// ensure/ proc exists

	if err := os.MkdirAll("/proc", 0755); err != nil {
		fmt.Fprintf(os.Stderr, "[init] mkdir /proc error: %v\n", err)
		os.Exit(1)
	}

	// 3. Mount proc

	//either isolate mount namespace or first unmpunt then mount

	//this is cause issue for host machine
	/*if err := syscall.Unmount("/proc", 0); err != nil {
		fmt.Println("Unmount error: ", err)
	}*/

	//EBUSY usually means it is already mounted
	// if errno, ok := err.(syscall.Errno); ok && errno == syscall.EBUSY {
	//  fmt.Println("/proc is already mounted")
	// } else {

	if err := syscall.Mount("proc", "/proc", "proc", syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_NODEV, ""); err != nil {
		fmt.Printf("[init] mount error:  %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[init] Mounted /proc")

	// 4.Exec real command

	/*
		Args[0] → binary name
		Args[1] → "child"
		Args[2:] → actual command (ls -l)
	*/

	// first validate args
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "[init] Invalid child invocation")
		os.Exit(1)
	}

	// Slice
	command := os.Args[3:] // Normally retrurn string, but we need slice

	if len(command) == 0 {
		fmt.Fprintln(os.Stderr, "[init] No command provided")
		os.Exit(1)
	}

	fmt.Println("[init] exec:", command)
	//crete a minimal env
	//Instead of os.Environ(), we define whats needed
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/root",
		"TERM=xterm",
		// Dynamically assign the container's PID-based hostname to the environment
		fmt.Sprintf("HOSTNAME=%s", hostName),
	}

	if err := syscall.Exec(command[0], command, env); err != nil {
		fmt.Fprintf(os.Stderr, "[init] exec error: %v\n", err)
		os.Exit(1)
	}
}

/*
Run()
  ↓
clone namespaces
  ↓
exec veil child
  ↓
Child()
  ↓
setup (hostname + mount)
  ↓
exec /bin/sh
*/