package monitoring

import (
	"fmt"
	"math/rand/v2"

	kubeapinodes "github.com/rancher/shepherd/extensions/kubeapi/nodes"
	corev1 "k8s.io/api/core/v1"
)

// PickNodeAddressWithType returns a random node address selected with the first preference
// type that yields at least one non-empty address across nodes, together with the address
// type that produced it; it errors when no preference type yields any address.
func PickNodeAddressWithType(nodes []corev1.Node, preference []corev1.NodeAddressType) (address string, addressType corev1.NodeAddressType, err error) {
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
			return candidates[rand.IntN(len(candidates))], addrType, nil
		}
	}

	return "", "", fmt.Errorf("no node addresses found matching address preference %v", preference)
}

// PickNodeAddress returns a random node address using the first preference type that
// yields at least one non-empty address across nodes; errors when none match.
func PickNodeAddress(nodes []corev1.Node, preference []corev1.NodeAddressType) (string, error) {
	address, _, err := PickNodeAddressWithType(nodes, preference)
	return address, err
}
