package monitoring

import (
	"strings"

	"github.com/rancher/tests/actions/registries"
)

// ResolveImageReference prefixes image with defaultRegistry when defaultRegistry is
// non-empty and the image reference has no registry host; otherwise returns image unchanged.
func ResolveImageReference(defaultRegistry, image string) string {
	if defaultRegistry == "" || registries.HasRegistryHost(image) {
		return image
	}

	return strings.TrimSuffix(defaultRegistry, "/") + "/" + image
}
