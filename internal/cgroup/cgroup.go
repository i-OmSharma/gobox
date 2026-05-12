// Package cgroup manages cgroups v2 resource limits for container processes.
//
// It creates a named cgroup slice under /sys/fs/cgroup, delegates the memory
// and cpu controllers from the parent, writes the requested limits, and moves
// the container process into the cgroup. On container exit, Remove() deletes
// the cgroup directory.
//
// cgroups v2 hierarchy used by veil:
//
//	/sys/fs/cgroup/          ← root cgroup (controllers delegated here)
//	└── veil-<pid>/          ← per-container cgroup created by Apply()
//	    ├── memory.max       ← hard RAM limit
//	    ├── memory.swap.max  ← swap cap (set to 0 to make memory.max enforceable)
//	    ├── cpu.max          ← "quota period" throttle
//	    ├── memory.oom_group ← kill whole cgroup on OOM (best-effort)
//	    └── cgroup.procs     ← container PID written here to attach it
package cgroup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const cgroupRoot = "/sys/fs/cgroup"

// CgroupConfig describes the resource limits for a single container's cgroup.
// Zero values mean "no limit" — Apply() skips writing that controller file.
type CgroupConfig struct {
	ID        string
	MemoryMax int64 // Hard RAM limit in bytes.
	CPUQuota  int64 // CPU time allowed per period, in microseconds.
	CPUPeriod int64 // Scheduling window length in microseconds (default 100ms = 100000).
}

// path returns the absolute path to this cgroup's directory.
func (c *CgroupConfig) path() string {
	return filepath.Join(cgroupRoot, c.ID)
}

// Apply creates the cgroup, enables controllers, writes limits, and attaches pid.
// Must be called while the container process is SIGSTOP'd — this ensures no
// child processes escape the cgroup before limits are in effect.
func (c *CgroupConfig) Apply(pid int) error {
	cgPath := c.path()

	if err := os.MkdirAll(cgPath, 0755); err != nil {
		return fmt.Errorf("mkdir cgroup: %w", err)
	}

	// cgroups v2: controllers must be listed in the parent's cgroup.subtree_control
	// before any child cgroup can use them. This is the delegation step.
	if err := enableControllers(cgroupRoot, "memory", "cpu"); err != nil {
		return fmt.Errorf("enable controllers: %w", err)
	}

	if c.MemoryMax > 0 {
		// memory.max: hard RAM limit — processes exceeding this are OOM-killed.
		if err := write(cgPath, "memory.max", fmt.Sprintf("%d", c.MemoryMax)); err != nil {
			return fmt.Errorf("memory.max: %w", err)
		}
		// memory.swap.max = 0: disable swap for this cgroup.
		// Without this, the kernel lets the cgroup overflow into swap and
		// memory.max becomes ineffective on systems that have swap enabled.
		if err := write(cgPath, "memory.swap.max", "0"); err != nil {
			fmt.Printf("[cgroup] warning: could not disable swap: %v\n", err)
		}
	}

	if c.CPUQuota > 0 && c.CPUPeriod > 0 {
		// cpu.max format: "quota period"
		// Example: "25000 100000" = 25% of one CPU core.
		if err := write(cgPath, "cpu.max", fmt.Sprintf("%d %d", c.CPUQuota, c.CPUPeriod)); err != nil {
			return fmt.Errorf("cpu.max: %w", err)
		}
	}

	// memory.oom_group = 1: kill the entire cgroup on OOM, not just one process.
	// Best-effort — older kernels or restrictive SELinux policies may deny this.
	if err := write(cgPath, "memory.oom_group", "1"); err != nil {
		fmt.Printf("[cgroup] warning: memory.oom_group not set: %v\n", err)
	}

	// cgroup.procs: move the container process into this cgroup.
	// All future child processes it spawns inherit this cgroup automatically.
	if err := write(cgPath, "cgroup.procs", fmt.Sprintf("%d", pid)); err != nil {
		return fmt.Errorf("attach process: %w", err)
	}

	return nil
}

// Remove deletes the cgroup directory after the container exits.
// Must be called after cmd.Wait() — the kernel refuses to remove a cgroup
// that still has live processes attached.
func (c *CgroupConfig) Remove() error {
	path := c.path()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	// Safety check — warn if stray processes are still attached (shouldn't happen after Wait).
	if data, err := os.ReadFile(filepath.Join(path, "cgroup.procs")); err == nil {
		if pids := strings.TrimSpace(string(data)); pids != "" {
			fmt.Printf("[cgroup] warning: processes still in cgroup %s: %s\n", c.ID, pids)
		}
	}

	// os.RemoveAll required — cgroup dirs contain kernel virtual files that must
	// be cleaned up as a group; os.Remove would fail on a non-empty directory.
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove cgroup %s: %w", c.ID, err)
	}
	return nil
}

// write writes value to a single cgroup control file.
func write(cgPath, file, value string) error {
	return os.WriteFile(filepath.Join(cgPath, file), []byte(value), 0644)
}

// enableControllers writes "+controller" entries to the parent's
// cgroup.subtree_control file, activating those controllers for child cgroups.
// Already-enabled controllers are skipped to avoid redundant writes.
func enableControllers(parentPath string, controllers ...string) error {
	subtreePath := filepath.Join(parentPath, "cgroup.subtree_control")
	current, err := os.ReadFile(subtreePath)
	if err != nil {
		return err
	}

	currentStr := string(current)
	var toEnable []string
	for _, ctrl := range controllers {
		if !strings.Contains(currentStr, ctrl) {
			toEnable = append(toEnable, "+"+ctrl)
		}
	}
	if len(toEnable) == 0 {
		return nil
	}

	return os.WriteFile(subtreePath, []byte(strings.Join(toEnable, " ")), 0644)
}
