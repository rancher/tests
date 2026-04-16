package autoscaling

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// HPAGroupVersionResource is the required Group Version Resource for accessing HorizontalPodAutoscalers in a cluster,
// using the dynamic client.
var HPAGroupVersionResource = schema.GroupVersionResource{
	Group:    "autoscaling",
	Version:  "v2",
	Resource: "horizontalpodautoscalers",
}
