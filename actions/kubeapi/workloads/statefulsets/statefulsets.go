package statefulsets

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// StatefulSetGroupVersionResource is the required Group Version Resource for accessing statefulsets in a cluster,
// using the dynamic client.
var StatefulSetGroupVersionResource = schema.GroupVersionResource{
	Group:    "apps",
	Version:  "v1",
	Resource: "statefulsets",
}
