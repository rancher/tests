//go:build validation

package rke

import (
	"os"
	"os/exec"
	"testing"

	upstream "github.com/qase-tms/qase-go/qase-api-client"
	"github.com/rancher/tests/actions/qase"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/suite"
)

type RKEValidationsTestSuite struct {
	suite.Suite
}

func (r *RKEValidationsTestSuite) TestRKEValidations() {
	rkeVersion := os.Getenv("RKE_VERSION")
	pemFile := os.Getenv("PEM_FILE")
	registry := os.Getenv("REGISTRY")
	registryUser := os.Getenv("REGISTRY_USER")
	registryPassword := os.Getenv("REGISTRY_PASSWORD")
	publicIP := os.Getenv("PUBLIC_IP")
	privateIP := os.Getenv("PRIVATE_IP")

	scriptPath := "./scripts/rke-validation.sh"

	tests := []struct {
		name       string
		rkeVersion string
	}{
		{
			name:       "RKE_Validations",
			rkeVersion: rkeVersion,
		},
	}

	for _, tt := range tests {
		r.Suite.T().Run(tt.name, func(t *testing.T) {
			commandArgs := []string{
				tt.rkeVersion,
				pemFile,
				registry,
				registryUser,
				registryPassword,
				publicIP,
				privateIP,
			}

			cmd := exec.Command(scriptPath, commandArgs...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			logrus.Infof("Running RKE validation script: %s", tt.name)

			err := cmd.Run()
			if err != nil {
				t.Fatalf("RKE validation script failed: %v", err)
			}
		})

		var params []upstream.TestCaseParameterCreate
		params = append(params, upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: "RKE Version", Values: []string{rkeVersion}}})
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestRKEValidationsTestSuite(t *testing.T) {
	suite.Run(t, new(RKEValidationsTestSuite))
}
