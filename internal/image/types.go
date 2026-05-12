// ImageRef represents a parsed OCi image reference
package image

import (
	"fmt"
	"github.com/google/go-containerregistry/pkg/name"
)

type ImageRef struct {
	Registry   string
	Repository string
	Tag        string
	Digest     string
	FullName   string
}

// ParseRef -> Parse a string reference into structured form
func ParseRef(ref string) (*ImageRef, error) {
	named, err := name.ParseReference(ref)
	if err != nil {
		return nil, fmt.Errorf("parse reference: %w", err)
	}

	return &ImageRef{
		Registry: named.Context().RegistryStr(),
		Repository: named.Context().RepositoryStr(),
		Tag: named.Identifier(),
		FullName: named.Name(),
	}, nil
}
