package overlayfs

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// OverlayFS represents an overlay mount for a container
type OverlayFS struct {
	ContainerID string
	LowerDir    string // Read-only image rootfs (e.g., /var/lib/veil/images/ubuntu/rootfs)
	BaseDir     string // /tmp/veil-<id>
}

// New creates a new OverlayFS instance
func New(containerID, lowerDir string) *OverlayFS {
	return &OverlayFS{
		ContainerID: containerID,
		LowerDir:    lowerDir,
		BaseDir:     filepath.Join("/tmp", "veil-"+containerID),
	}
}

// Mount sets up the OverlayFS and returns the merged directory path
func (o *OverlayFS) Mount() (string, error) {
	upper := filepath.Join(o.BaseDir, "upper")
	work := filepath.Join(o.BaseDir, "work")
	merged := filepath.Join(o.BaseDir, "merged")

	// Create directories
	for _, dir := range []string{upper, work, merged} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	// Build mount options (NO SPACES in opts string!)
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s",
		o.LowerDir, upper, work)

	// Mount OverlayFS
	if err := syscall.Mount("overlay", merged, "overlay", 0, opts); err != nil {
		return "", fmt.Errorf("mount overlay: %w", err)
	}

	return merged, nil
}

// Unmount cleans up the OverlayFS mount and directories
func (o *OverlayFS) Unmount() error {
	merged := filepath.Join(o.BaseDir, "merged")

	// Unmount the overlay
	if err := syscall.Unmount(merged, syscall.MNT_DETACH); err != nil {
		return fmt.Errorf("unmount overlay: %w", err)
	}

	// Remove the base directory (upper, work, merged)
	if err := os.RemoveAll(o.BaseDir); err != nil {
		return fmt.Errorf("remove base dir: %w", err)
	}

	return nil
}

// PivotRoot switches the root filesystem to newRoot
// MUST be called from inside the container process (Child())
func PivotRoot(newRoot string) error {
	// Step 1: Bind mount newRoot onto itself (kernel requirement for pivot_root)
	if err := syscall.Mount(newRoot, newRoot, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		return fmt.Errorf("bind mount: %w", err)
	}

	// Step 2: Create directory for old root
	oldRoot := filepath.Join(newRoot, ".old_root")
	if err := os.MkdirAll(oldRoot, 0700); err != nil {
		return fmt.Errorf("mkdir old_root: %w", err)
	}

	// Step 3: Pivot root - newRoot becomes /, old / moves to .old_root
	if err := syscall.PivotRoot(newRoot, oldRoot); err != nil {
		return fmt.Errorf("pivot_root: %w", err)
	}

	// Step 4: Change to new root
	if err := syscall.Chdir("/"); err != nil {
		return fmt.Errorf("chdir: %w", err)
	}

	// Step 5: Unmount old root (now at /.old_root)
	if err := syscall.Unmount("/.old_root", syscall.MNT_DETACH); err != nil {
		return fmt.Errorf("unmount old_root: %w", err)
	}

	// Step 6: Remove old_root directory
	if err := os.Remove("/.old_root"); err != nil {
		return fmt.Errorf("remove old_root: %w", err)
	}

	return nil
}
