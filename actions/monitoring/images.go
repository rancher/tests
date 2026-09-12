package monitoring

import "strings"

// ResolveImageReference prefixes image with defaultRegistry when defaultRegistry is
// non-empty and the image reference has no registry host; otherwise returns image unchanged.
func ResolveImageReference(defaultRegistry, image string) string {
	if defaultRegistry == "" || hasRegistryHost(image) {
		return image
	}

	return strings.TrimSuffix(defaultRegistry, "/") + "/" + image
}

// hasRegistryHost reports whether the image reference starts with an explicit registry host,
// following the Docker convention: the first path component counts as a host when it
// contains a "." or ":" (domain or port) or is "localhost".
func hasRegistryHost(image string) bool {
	first, _, found := strings.Cut(image, "/")
	if !found {
		return false
	}

	return first == "localhost" || strings.ContainsAny(first, ".:")
}
