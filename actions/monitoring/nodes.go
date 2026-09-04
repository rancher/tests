package monitoring

import (
	"fmt"
	"math/rand/v2"

	kubeapinodes "github.com/rancher/shepherd/extensions/kubeapi/nodes"
	corev1 "k8s.io/api/core/v1"
)

// PickNodeAddress returns a random node address using the first preference type that
// yields at least one non-empty address across nodes; errors when none match.
func PickNodeAddress(nodes []corev1.Node, preference []corev1.NodeAddressType) (string, error) {
	for _, addrType := range preference {
		candidates := []string{}
		for i := range nodes {
			address := kubeapinodes.GetNodeIP(&nodes[i], addrType)
			if address == "" {
				continue
			}

			candidates = append(candidates, address)
		}

		if len(candidates) > 0 {
			return candidates[rand.IntN(len(candidates))], nil
		}
	}

	return "", fmt.Errorf("no node addresses found matching address preference %v", preference)
}
