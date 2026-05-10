
package cgroup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const cgroupRoot = "/sys/fs/cgroup"

type CgroupConfig struct {
	ID        string
	MemoryMax int64 //in byte
	CPUQuota  int64 //in microseconds
	CPUPeriod int64 //in microseconds
}

// path returns the full path to the cgroup directory
func (c *CgroupConfig) path() string { // we have taken Pointers here using pointer receiver (*CgroupConfig) to avoid copying the struct on every call.
	return filepath.Join(cgroupRoot, c.ID)
}
//Actually, path() doesn't modify the struct, so a value receiver func (c CgroupConfig) path() is also fine and slightly more efficient for small structs. 
// However, keeping it as a pointer receiver is standard practice if other methods (like Apply) modify state or if you want consistency. Just note that path() itself is read-only.

// Apply sets up the cgroup and moves the process into it
func (c *CgroupConfig) Apply(pid int) error {
	cgPath := c.path()
	

	// Create cgroup Dir
	if err := os.MkdirAll(cgPath, 0755); err != nil {
		return fmt.Errorf("mkdir error: %w", err)
	}

	//Enable controllers in the parent cgroup
	// In cgroups v2, controllers must be explicitly enabled in the parent's
	// cgroup.subtree_control file before they can be used in child cgroups.
	if err := enableControllers(cgroupRoot, "memory", "cpu"); err != nil {
		return fmt.Errorf("Enable controller: %w", err)
	}

	// TODO 1: memory.max
	// Set the hard memory limit. "max" means no limit, otherwise specify bytes.
	// memFile := filepath.Join(cgPath, "memory.max")

	if c.MemoryMax > 0 {
		if err := write(cgPath, "memory.max", fmt.Sprintf("%d", c.MemoryMax)); err != nil {
			return fmt.Errorf("Set memory.max: %w", err)
		}
	}

	// TODO 2: cpu.max
	// Format: "quota period".
	// quota: how much CPU time (in microseconds) the group can use within a period.
	// period: the length of the window (in microseconds).
	// Example: "50000 100000" means 50% CPU usage.

	if c.CPUQuota > 0 && c.CPUPeriod > 0 {
		val := fmt.Sprintf("%d %d", c.CPUQuota, c.CPUPeriod)
		if err := write(cgPath, "cpu.max", val); err != nil {
			return fmt.Errorf("Set cpu.max: %w", err)
		}
	}

	// TODO 3: Set OOM Group (NON-CRITICAL / BEST EFFORT)
	// Docker does this: It tries to set it, but if it fails (permissions/old kernel),
	// it logs a warning and keeps going. The container still runs, just without 
	// the "kill-all-on-OOM" guarantee.

	if err := write(cgPath, "memory.oom_group", "1"); err != nil {
		fmt.Printf("[cgroup] Warning: Failed to set memory.oom_group: %v. Container will run with default OOM behavior.\n", err)
	}

	// TODO 4: cgroup.procs
	// Attach the process to the cgroup. This moves the PID into the group.
	if err := write(cgPath, "cgroup.procs", fmt.Sprintf("%d", pid)); err != nil {
		return fmt.Errorf("Add process to cgroup: %w", err)
	}

	return nil
}

//cleanUp

func (c *CgroupConfig) Remove() error {
	path := c.path() //Fixed variable name

	// 1. Check if the cgroup directory exists
	if _,err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}



	// 2. Safety Net: Check for remaining processes
	// If the container crashed, processes might still be here.
	// os.RemoveAll will fail if processes are present, so we warn the user.
	procsFile := filepath.Join(path, "cgroup.procs")
	data, err := os.ReadFile(procsFile)
	if err == nil {
		pids := strings.TrimSpace(string(data))
		if pids != "" {
			fmt.Printf("[cgroup] Warning: Processes still in cgroup %s: %s\n", c.ID, pids)
			// In a production runtime, you would iterate these PIDs and kill them here.
		}
	}

	// Use RemoveAll because cgroup directories contain virtual files 
	// (like memory.max, cpu.stat) that must be cleaned up.
	// Note: This will fail if processes are still attached. 
	// Ensure you call this AFTER cmd.Wait() in container.go.
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove cgroup %s: %w", path, err)
	}
	return nil
}

// func (c *CgroupConfig) Remove() error {
// 	paht := c.path()
// 	// Check if exists first to avoid unecessary errors
// 	if _, err := os.Stat(path); os.IsNotExist(err) {
// 		return nil
// 	}
// 	return os.Remove(c.path()) // os.Remove only removes empty directories. If the cgroup still has processes or sub-cgroups, this will fail.
// 	return os.RemoveAll(path) // RemoveAll is safer, but will fail if processes are still inside.
// }

// write helper to write string values to cgroup files

func write(path, file, value string) error {
	return os.WriteFile(
		filepath.Join(path, file),
		[]byte(value),
		0644,
	)
}

// enableControllers delegates controllers from parent to child

func enableControllers(parentPath string, controllers ...string) error {
	subtreeControlpath := filepath.Join(parentPath, "cgroup.subtree_control")

	curent, err := os.ReadFile(subtreeControlpath)
	if err != nil {
		return err
	}

	curentStr := string(curent)
	var toEnable []string

	for _, ctrl := range controllers {
		// Check if already enabled to avoid writing duplicates
		if !strings.Contains(curentStr, "+"+ctrl){
			toEnable = append(toEnable, "+"+ctrl)
		}
	}

	if len(toEnable) == 0 {
		return nil
	}

	if err :=  os.WriteFile(subtreeControlpath, []byte(strings.Join(toEnable, " ")), 0644); err != nil {
		return fmt.Errorf("Write subtree_control: %w", err)
	}

	return nil
}

