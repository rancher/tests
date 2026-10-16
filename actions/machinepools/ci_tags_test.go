package machinepools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func setCIEnv(t *testing.T, env map[string]string) {
	for _, key := range []string{"GITHUB_ACTIONS", "GITHUB_REPOSITORY", "GITHUB_WORKFLOW", "GITHUB_WORKFLOW_REF", "GITHUB_RUN_ID", "JOB_NAME", "BUILD_NUMBER"} {
		t.Setenv(key, env[key])
	}
}

func TestCIMachineConfigTags(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		configured string
		expected   string
	}{
		{
			name: "github actions",
			env: map[string]string{
				"GITHUB_ACTIONS":      "true",
				"GITHUB_REPOSITORY":   "rancher/tests",
				"GITHUB_WORKFLOW_REF": "rancher/tests/.github/workflows/cluster-provisioning.yml@refs/heads/main",
				"GITHUB_RUN_ID":       "123456",
			},
			expected: "ci_build,123456,ci_job,rancher/tests/cluster-provisioning.yml",
		},
		{
			name:       "jenkins with configured tags",
			env:        map[string]string{"JOB_NAME": "folder/my,job (nightly)", "BUILD_NUMBER": "42"},
			configured: "owner,abc",
			expected:   "owner,abc,ci_build,42,ci_job,folder/my-job -nightly-",
		},
		{
			name:       "local run",
			env:        map[string]string{},
			configured: "owner,abc",
			expected:   "owner,abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setCIEnv(t, tt.env)
			assert.Equal(t, tt.expected, mergeMachineConfigTags(tt.configured, ciMachineConfigTags()))
		})
	}
}
