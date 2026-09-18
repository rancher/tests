package hosted

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/cloudcredentials/aws"
	"github.com/rancher/shepherd/extensions/cloudcredentials/azure"
	"github.com/rancher/shepherd/extensions/cloudcredentials/google"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// WaitForClusterToAppear is a helper function that polls the provisioning.cattle.io Steve API until the given cluster ID
// is retrievable.
func WaitForClusterToAppear(client *rancher.Client, clusterID string) (*steveV1.SteveAPIObject, error) {
	var cluster *steveV1.SteveAPIObject

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.FiveMinuteTimeout, false, func(context.Context) (bool, error) {
		var pollErr error
		cluster, pollErr = client.Steve.SteveType(stevetypes.Provisioning).ByID(clusterID)
		if pollErr != nil {
			if strings.Contains(pollErr.Error(), "404") {
				return false, nil
			}

			return false, pollErr
		}

		return true, nil
	})

	return cluster, err
}

// CreateHostedClusterCredential is a helper function that creates cloud credentials for hosted clusters based on the provider specified.
func CreateHostedClusterCredential(client *rancher.Client, provider string) (string, error) {
	switch provider {
	case "aks":
		cloudCredential, err := azure.CreateAzureCloudCredentials(client, cloudcredentials.LoadCloudCredential("azure"))
		if err != nil {
			return "", err
		}

		return cloudCredential.Namespace + ":" + cloudCredential.Name, nil
	case "eks":
		cloudCredential, err := aws.CreateAWSCloudCredentials(client, cloudcredentials.LoadCloudCredential("aws"))
		if err != nil {
			return "", err
		}

		return cloudCredential.Namespace + ":" + cloudCredential.Name, nil
	case "gke":
		credential := cloudcredentials.LoadCloudCredential("google")
		if credential.GoogleCredentialConfig == nil {
			return "", fmt.Errorf("google credential config is missing")
		}

		var serviceAccount map[string]any
		if err := json.Unmarshal([]byte(credential.GoogleCredentialConfig.AuthEncodedJSON), &serviceAccount); err != nil {
			return "", fmt.Errorf("invalid googleCredentials.authEncodedJson: %w", err)
		}

		cloudCredential, err := google.CreateGoogleCloudCredentials(client, credential)
		if err != nil {
			return "", err
		}

		return cloudCredential.Namespace + ":" + cloudCredential.Name, nil
	default:
		return "", fmt.Errorf("invalid hosted provider %q; valid providers: aks, eks, gke", provider)
	}
}
