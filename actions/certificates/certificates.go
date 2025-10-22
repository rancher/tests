package certificates

import (
	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/pkg/wait"
	"k8s.io/apimachinery/pkg/watch"
)

// CertRotationCompleteCheckFunc returns a watch check function that checks if the certificate rotation is complete
func CertRotationCompleteCheckFunc(generation int64) wait.WatchCheckFunc {
	return func(event watch.Event) (bool, error) {
		controlPlane := event.Object.(*rkev1.RKEControlPlane)
		return controlPlane.Status.CertificateRotationGeneration == generation, nil
	}
}
