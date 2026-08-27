package monitoring

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func nodeWithAddresses(name string, addresses ...corev1.NodeAddress) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Addresses: addresses,
		},
	}
}

func defaultPreference() []corev1.NodeAddressType {
	return []corev1.NodeAddressType{corev1.NodeExternalIP, corev1.NodeInternalIP}
}

func TestPickNodeAddress(t *testing.T) {
	external := corev1.NodeAddress{Type: corev1.NodeExternalIP, Address: "203.0.113.10"}
	internal := corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "192.168.1.10"}

	tests := []struct {
		name       string
		nodes      []corev1.Node
		preference []corev1.NodeAddressType
		want       string
		wantErr    bool
	}{
		{
			name:       "external present is picked under default preference",
			nodes:      []corev1.Node{nodeWithAddresses("node1", external, internal)},
			preference: defaultPreference(),
			want:       "203.0.113.10",
		},
		{
			name:       "internal picked when external absent",
			nodes:      []corev1.Node{nodeWithAddresses("node1", internal)},
			preference: defaultPreference(),
			want:       "192.168.1.10",
		},
		{
			name:       "no addresses at all errors",
			nodes:      []corev1.Node{nodeWithAddresses("node1")},
			preference: defaultPreference(),
			wantErr:    true,
		},
		{
			name:       "inverted preference picks internal when both present",
			nodes:      []corev1.Node{nodeWithAddresses("node1", external, internal)},
			preference: []corev1.NodeAddressType{corev1.NodeInternalIP, corev1.NodeExternalIP},
			want:       "192.168.1.10",
		},
		{
			name: "nodes without addresses are skipped",
			nodes: []corev1.Node{
				nodeWithAddresses("node1"),
				nodeWithAddresses("node2", internal),
			},
			preference: defaultPreference(),
			want:       "192.168.1.10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PickNodeAddress(tt.nodes, tt.preference)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
