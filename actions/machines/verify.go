package machines

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/sirupsen/logrus"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// VerifyMachineReplacement verifies that a machine has been successfully replaced by checking for its deletion.
func VerifyMachineReplacement(client *rancher.Client, replacedMachine *v1.SteveAPIObject) error {
	err := kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.TenMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		_, err = client.Steve.SteveType(stevetypes.Machine).ByID(replacedMachine.ID)
		if err != nil {
			logrus.Infof("Node has successfully been deleted!")
			return true, nil
		}

		return false, nil
	})

	if err != nil {
		return fmt.Errorf("error while waiting for machine to be deleted: %w", err)
	}

	return nil
}
