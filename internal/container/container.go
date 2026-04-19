//Orchestration Layer

package container

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// “Run = create + configure + start container process”
func Run(image string, command []string) {
	fmt.Println("[container] Starting...")
	fmt.Println("Image:", image)
	fmt.Println("Command:", command)

	// Safyty Check
	if len(command) == 0 {
		fmt.Fprintln(os.Stderr, "[container] No command provided")
		os.Exit(1)
	}

	// Create Command
	// cmd := exec.Command(command[0], command[1:]...) //runs directly(/bin/sh) child was never called

	//Re-exec same niamry in new namespace
	cmd := exec.Command("/proc/self/exe", append([]string{"child", image}, command...)...) //same binary run again(/proc/self/exe) -> gobox child /bin/sh

	// Attach Terminal
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// mark as child (to avoid duplicate logs)
	cmd.Env = append(os.Environ(), "GOBOX_CHILD=1")

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

	// Run process
	if err := cmd.Run(); err != nil {
		fmt.Printf("[container]Error : %v\n", err)
		os.Exit(1)
	}
}

func Child() {

	fmt.Println("[init] Inside container init")

	// validate args
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "[init] Invalid child invocation")
		os.Exit(1)
	}
	// TODO:

	// 1. Isolate mount namespace

	if err := syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""); err != nil {
		fmt.Fprintf(os.Stderr, "[init] mount propagation error: %v\n", err)
		os.Exit(1)
	}

	//Set hostname

	HostName := fmt.Sprintf("gobox-%d", os.Getpid())
	if err := syscall.Sethostname([]byte(HostName)); err != nil {
		fmt.Fprintf(os.Stderr, "[init] HostName error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[init] Hostname set to:", HostName)

	// ensure/ proc exists

	if err := os.MkdirAll("/proc", 0755); err != nil {
		fmt.Fprintf(os.Stderr, "[init] mkdir /proc error: %v\n", err)
		os.Exit(1)
	}

	//mount proc

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
		// }
	}
	fmt.Println("[init] Mounted /proc")

	//exec real command

	/*
		Args[0] → binary name
		Args[1] → "child"
		Args[2:] → actual command (ls -l)
	*/

	// first validate
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

	if err := syscall.Exec(command[0], command, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "[init] exec error: %v\n", err)
		os.Exit(1)
	}
}


/*
Run()
  ↓
clone namespaces
  ↓
exec gobox child
  ↓
Child()
  ↓
setup (hostname + mount)
  ↓
exec /bin/sh
*/