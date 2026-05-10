//Orchestration Layer

package container

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"github.com/i-OmSharma/veil/internal/cgroup"
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

	// Create Command
	// cmd := exec.Command(command[0], command[1:]...) //runs directly(/bin/sh) child was never called

	//We re-exec the same binary with "child" argument to enter the namespace
	cmd := exec.Command("/proc/self/exe", append([]string{"child", image}, command...)...) //same binary run again(/proc/self/exe) -> veil child /bin/sh

	// Attach Terminal
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "VEIL_CHILD=1") // mark as child (to avoid duplicate logs) in the child() function

	//NameSpcace isolation
	// Cloneflags = used to run process in a isolated world.

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWPID |
			syscall.CLONE_NEWUTS |
			syscall.CLONE_NEWNS |
			syscall.CLONE_NEWIPC,
		/////////////////////////////
		// syscall.CLONE_NEWCGROUP |
		// syscall.CLONE_NEWNET |
		// syscall.CLONE_NEWUSER |
		// syscall.CLONE_NEWTIME,
	}

	// 1. Start the rpocess(Npn-Blocking)
	// We use Start() instead of Run() so we can interact with the PID before it fully runs
	if err := cmd.Start(); err != nil {
		fmt.Printf("[container] strat error: %v\n", err)
		os.Exit(1)
	} 

	/*
	// Run process
	if err := cmd.Run(); err != nil {
		fmt.Printf("[container]Error : %v\n", err)
		os.Exit(1)
	}
		*/   //this run was previously used, it was blocking
		
	// 2. Pause Process Immediately (safety Critical)
	// We freeze the process so it doesn't consume resources before limits are applied
	if err:=  cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		fmt.Printf("[container] failed to stop process: %v\n", err)
		cmd.Process.Kill()
		os.Exit(1)
	}

	// 3. Apply Dynamic Cgroup WHILE Paused
	pid := cmd.Process.Pid
	cgConfig := &cgroup.CgroupConfig{
		ID: fmt.Sprintf("veil-%d", pid),
		MemoryMax: resources.MemoryMax, // DYNAMIC VALUE
		CPUQuota: resources.CPUQuota, // DYNAMIC VALUE
		CPUPeriod: resources.CPUPeriod, // DYNAMIC VALUE
	}

	//Apply cGroup before resuming
	// Only apply if limits are actually set
	if cgConfig.MemoryMax > 0 || cgConfig.CPUQuota > 0 {
		if err := cgConfig.Apply(pid); err != nil {
			fmt.Printf("[container] cgroup apply error: %v\n", err)
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

	// 5. Wait for Completion
	if err := cmd.Wait(); err != nil {
		fmt.Printf("[container] exit error: %v\n", err)
	}

	// 6. Cleanup Cgroups
	if err := cgConfig.Remove(); err != nil {
		fmt.Printf("[container] cleanup warning: %v\n", err)
	} else {
		fmt.Println("[container] Cgroups cleaned up")
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
		// 	fmt.Println("/proc is already mounted")
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
		"HOSTNAME=veil-container",
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