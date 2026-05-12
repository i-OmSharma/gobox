package image

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

const ImageStoreRoot = "/var/lib/veil/images"

// Pull downloads an OCI image and returns the path to its extracted rootfs
func Pull(imageRef string) (string, error) {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return "", fmt.Errorf("parse reference: %w", err)
	}

	// Create storage directory
	imageDir := filepath.Join(ImageStoreRoot, sanitizeRef(ref.Name()))
	if err := os.MkdirAll(imageDir, 0755); err != nil {
		return "", fmt.Errorf("mkdir image dir: %w", err)
	}

	// Check cache
	rootfsPath := filepath.Join(imageDir, "rootfs")
	if _, err := os.Stat(rootfsPath); err == nil {
		fmt.Printf("[image] Using cached: %s\n", imageRef)
		return rootfsPath, nil
	}

	fmt.Printf("[image] Pulling %s...\n", imageRef)

	// Pull image with auth support
	img, err := crane.Pull(imageRef, crane.WithAuthFromKeychain(authn.DefaultKeychain))
	if err != nil {
		return "", fmt.Errorf("pull image: %w", err)
	}

	// Extract all layers to rootfs
	if err := extractLayers(img, rootfsPath); err != nil {
		return "", fmt.Errorf("extract layers: %w", err)
	}

	fmt.Printf("[image] Pulled: %s → %s\n", imageRef, rootfsPath)
	return rootfsPath, nil
}

// extractLayers downloads and extracts all image layers in order
func extractLayers(img v1.Image, dest string) error {
	layers, err := img.Layers()
	if err != nil {
		return fmt.Errorf("get layers: %w", err)
	}

	for i, layer := range layers {
		fmt.Printf("[image] Extracting layer %d/%d...\n", i+1, len(layers))

		rc, err := layer.Uncompressed()
		if err != nil {
			return fmt.Errorf("get layer stream: %w", err)
		}

		if err := extractTar(rc, dest); err != nil {
			rc.Close()
			return fmt.Errorf("extract tar: %w", err)
		}
		rc.Close()
	}
	return nil
}

// extractTar extracts a tar stream to destination directory
func extractTar(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	dest = filepath.Clean(dest)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Security: Prevent path traversal attacks
		target := filepath.Join(dest, header.Name)
		if !strings.HasPrefix(target, dest+string(os.PathSeparator)) && target != dest {
			return fmt.Errorf("illegal path: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)&0777); err != nil {
				return err
			}
		case tar.TypeReg:
			// Handle OCI whiteout files (.wh.<filename>)
			baseName := filepath.Base(header.Name)
			if strings.HasPrefix(baseName, ".wh.") {
				realFile := filepath.Join(filepath.Dir(target), strings.TrimPrefix(baseName, ".wh."))
				if err := os.Remove(realFile); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("remove whiteout target: %w", err)
				}
				continue // Don't create the .wh. file itself
			}

			// Create parent dirs
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, os.FileMode(header.Mode)&0777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			// Absolute symlinks (e.g. /bin/busybox) resolve inside the container's
			// rootfs after pivot_root — not on the host. Always safe to allow.
			// Relative symlinks are validated: if they escape dest during extraction
			// they could point at host files when followed by subsequent tar entries.
			if !filepath.IsAbs(header.Linkname) {
				resolved := filepath.Clean(filepath.Join(filepath.Dir(target), header.Linkname))
				if !strings.HasPrefix(resolved, dest+string(os.PathSeparator)) && resolved != dest {
					return fmt.Errorf("illegal relative symlink: %s -> %s", header.Name, header.Linkname)
				}
			}
			if err := os.Symlink(header.Linkname, target); err != nil && !os.IsExist(err) {
				return err
			}
		}
	}
	return nil
}

// Push uploads a directory as a new OCI image to a registry
func Push(sourceDir, imageRef string) error {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return fmt.Errorf("parse reference: %w", err)
	}

	fmt.Printf("[image] Pushing %s to %s...\n", sourceDir, imageRef)

	// Create a new empty image
	img := empty.Image

	// Create a layer from the source directory
	layer, err := tarball.LayerFromFile(sourceDir, tarball.WithCompressedCaching)
	if err != nil {
		return fmt.Errorf("create layer: %w", err)
	}

	// Add layer to image
	img, err = mutate.AppendLayers(img, layer)
	if err != nil {
		return fmt.Errorf("append layer: %w", err)
	}

	// Add basic config
	configFile := &v1.ConfigFile{
		Config: v1.Config{
			Cmd: []string{"/bin/sh"},
		},
	}
	img, err = mutate.ConfigFile(img, configFile)
	if err != nil {
		return fmt.Errorf("set config: %w", err)
	}

	// Push to registry with auth
	if err := remote.Write(ref, img, remote.WithAuthFromKeychain(authn.DefaultKeychain)); err != nil {
		return fmt.Errorf("push image: %w", err)
	}

	fmt.Printf("[image] Pushed: %s\n", imageRef)
	return nil
}

// ListLocalImages returns all locally cached images
func ListLocalImages() ([]string, error) {
	var images []string

	entries, err := os.ReadDir(ImageStoreRoot)
	if os.IsNotExist(err) {
		return images, nil
	}
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			images = append(images, entry.Name())
		}
	}
	return images, nil
}

// sanitizeRef converts image ref to filesystem-safe path
func sanitizeRef(ref string) string {
	replacer := strings.NewReplacer(
		"/", "__",
		":", "__",
		"@", "__",
	)
	return replacer.Replace(ref)
}

// Resolve returns the rootfs path for an image, pulling if needed
func Resolve(imageRef string) (string, error) {
	return Pull(imageRef)
}