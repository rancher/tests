package pods

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// PodGroupVersionResource is the required Group Version Resource for accessing Pods in a cluster,
// using the dynamic client.
var PodGroupVersionResource = schema.GroupVersionResource{
	Group:    "",
	Version:  "v1",
	Resource: "pods",
}
