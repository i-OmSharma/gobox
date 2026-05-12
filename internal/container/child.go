// child.go — the container's init process.
//
// Child() runs INSIDE the new namespaces after the binary re-execs itself.
// It is the first (and only) process in the new PID namespace — PID 1.
// Its job: set up the container environment, then exec the user's command.
//
// Call sequence:
//
//	Child()
//	  → isolate mounts (MS_PRIVATE)
//	  → set hostname
//	  → bind-mount /dev
//	  → pivot_root to container rootfs
//	  → mount /proc
//	  → exec user command
package container

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/i-OmSharma/veil/internal/overlayfs"
)

// Child is invoked when the veil binary is re-exec'd with "child" as the first arg.
// It never returns — it either calls syscall.Exec (replacing itself with the user command)
// or calls os.Exit on failure.
//
// Expected os.Args layout:
//
//	[0] /proc/self/exe   (binary path)
//	[1] "child"          (sentinel — triggers this path in main)
//	[2] image            (image reference, unused here)
//	[3:] command         (the command to exec inside the container)
func Child() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "[init] invalid invocation — expected: child <image> <command...>")
		os.Exit(1)
	}

	// VEIL_ROOTFS is set by Run() before Start() — it points to the overlay merged dir.
	rootfs := os.Getenv("VEIL_ROOTFS")
	if rootfs == "" {
		fmt.Fprintln(os.Stderr, "[init] VEIL_ROOTFS not set")
		os.Exit(1)
	}

	// Make the mount namespace private so no mount/unmount events propagate to the host.
	// MS_PRIVATE|MS_REC applies to this mount point and all its submounts.
	if err := syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""); err != nil {
		fmt.Fprintf(os.Stderr, "[init] mount propagation: %v\n", err)
		os.Exit(1)
	}

	// Set the container hostname — visible only inside this UTS namespace.
	// We use the container's PID as a suffix so each container has a unique name.
	hostname := fmt.Sprintf("veil-%d", os.Getpid())
	if err := syscall.Sethostname([]byte(hostname)); err != nil {
		fmt.Fprintf(os.Stderr, "[init] sethostname: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[init] hostname:", hostname)

	// Bind-mount the host's /dev into the container rootfs before pivot_root.
	// After pivot_root the old host paths are gone, so /dev must be wired in now.
	// This gives the container access to /dev/null, /dev/zero, /dev/urandom etc.
	// Production runtimes build a filtered /dev; for v0.1 we reuse the host's.
	devTarget := filepath.Join(rootfs, "dev")
	if err := os.MkdirAll(devTarget, 0755); err == nil {
		if err := syscall.Mount("/dev", devTarget, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
			fmt.Printf("[init] /dev bind warning: %v\n", err)
		}
	}

	// pivot_root: swap the container rootfs for the host's /.
	// This must happen BEFORE mounting /proc — the new /proc belongs on the new root.
	if err := overlayfs.PivotRoot(rootfs); err != nil {
		fmt.Fprintf(os.Stderr, "[init] pivot_root: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[init] pivoted to rootfs")

	// Mount a fresh /proc for this PID namespace.
	// MS_NOEXEC, MS_NOSUID, MS_NODEV are standard hardening flags for /proc.
	if err := os.MkdirAll("/proc", 0755); err != nil {
		fmt.Fprintf(os.Stderr, "[init] mkdir /proc: %v\n", err)
		os.Exit(1)
	}
	if err := syscall.Mount("proc", "/proc", "proc",
		syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_NODEV, ""); err != nil {
		fmt.Fprintf(os.Stderr, "[init] mount /proc: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[init] mounted /proc")

	// Everything below /proc is now the container's own view — exec the user's command.
	command := os.Args[3:]
	if len(command) == 0 {
		fmt.Fprintln(os.Stderr, "[init] no command to execute")
		os.Exit(1)
	}

	// Minimal, explicit environment — never inherit host variables.
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/root",
		"TERM=xterm",
		fmt.Sprintf("HOSTNAME=%s", hostname),
	}

	fmt.Println("[init] exec:", command)

	// syscall.Exec replaces this process image — if it returns, something went wrong.
	if err := syscall.Exec(command[0], command, env); err != nil {
		fmt.Fprintf(os.Stderr, "[init] exec: %v\n", err)
		os.Exit(1)
	}
}
