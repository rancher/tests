//go:build !sanity && !extended && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13 && !2.14

package charts

import (
	"context"
	"testing"

	managementv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/kubeconfig"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	authUserInfoMaxAgeSecondsSetting = "auth-user-info-max-age-seconds"
	authUserInfoMaxAgeSecondsDefault = "3600"
	settingsValidatingWebhookName    = "rancher.cattle.io.settings.management.cattle.io"
)

var managementSettingGVR = schema.GroupVersionResource{
	Group:    "management.cattle.io",
	Version:  "v3",
	Resource: "settings",
}

type authUserInfoMaxAgeTestCase struct {
	name         string
	value        string
	defaultValue string
	allowed      bool
}

var authUserInfoMaxAgeTestCases = []authUserInfoMaxAgeTestCase{
	{
		name:    "ValidMaxAge",
		value:   "3600",
		allowed: true,
	},
	{
		name:    "ValidNegativeMaxAge",
		value:   "-1",
		allowed: true,
	},
	{
		name:  "InvalidMaxAge",
		value: "foo",
	},
	{
		name:         "EmptyValueWithDefault",
		defaultValue: authUserInfoMaxAgeSecondsDefault,
		allowed:      true,
	},
	{
		name: "EmptyValueAndDefault",
	},
}

type WebhookAuthUserInfoMaxAgeTestSuite struct {
	suite.Suite
	client   *rancher.Client
	session  *session.Session
	settings dynamic.ResourceInterface
}

func (w *WebhookAuthUserInfoMaxAgeTestSuite) TearDownSuite() {
	w.session.Cleanup()
}

func (w *WebhookAuthUserInfoMaxAgeTestSuite) SetupSuite() {
	w.session = session.NewSession()

	client, err := rancher.NewClient("", w.session)
	require.NoError(w.T(), err)
	w.client = client

	webhookNames, err := getWebhookNames(w.client, localCluster, resourceName)
	require.NoError(w.T(), err)
	require.Contains(w.T(), webhookNames, settingsValidatingWebhookName)

	kubeConfig, err := kubeconfig.GetKubeconfig(w.client, localCluster)
	require.NoError(w.T(), err)
	require.NotNil(w.T(), kubeConfig)

	restConfig, err := (*kubeConfig).ClientConfig()
	require.NoError(w.T(), err)

	dynamicClient, err := dynamic.NewForConfig(restConfig)
	require.NoError(w.T(), err)
	w.settings = dynamicClient.Resource(managementSettingGVR)
}

func (w *WebhookAuthUserInfoMaxAgeTestSuite) TestAuthUserInfoMaxAgeSecondsOnCreate() {
	ctx := context.Background()

	for _, testCase := range authUserInfoMaxAgeTestCases {
		testCase := testCase
		w.Run(testCase.name, func() {
			settingObject, err := settingToUnstructured(newAuthUserInfoMaxAgeSetting(testCase.value, testCase.defaultValue))
			require.NoError(w.T(), err)

			_, err = w.settings.Create(ctx, settingObject, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
			requireAuthUserInfoMaxAgeResult(w.T(), testCase, err, true)
		})
	}
}

func (w *WebhookAuthUserInfoMaxAgeTestSuite) TestAuthUserInfoMaxAgeSecondsOnUpdate() {
	ctx := context.Background()

	for _, testCase := range authUserInfoMaxAgeTestCases {
		testCase := testCase
		w.Run(testCase.name, func() {
			settingObject, err := w.settings.Get(ctx, authUserInfoMaxAgeSecondsSetting, metav1.GetOptions{})
			require.NoError(w.T(), err)

			settingObject.Object["value"] = testCase.value
			settingObject.Object["default"] = testCase.defaultValue
			_, err = w.settings.Update(ctx, settingObject, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
			requireAuthUserInfoMaxAgeResult(w.T(), testCase, err, false)
		})
	}
}

func newAuthUserInfoMaxAgeSetting(value, defaultValue string) *managementv3.Setting {
	return &managementv3.Setting{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "management.cattle.io/v3",
			Kind:       "Setting",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: authUserInfoMaxAgeSecondsSetting,
		},
		Value:   value,
		Default: defaultValue,
	}
}

func requireAuthUserInfoMaxAgeResult(t *testing.T, testCase authUserInfoMaxAgeTestCase, err error, allowAlreadyExists bool) {
	if testCase.allowed {
		if allowAlreadyExists && apierrors.IsAlreadyExists(err) {
			return
		}
		require.NoError(t, err)
		return
	}

	require.Error(t, err)
	require.Contains(t, err.Error(), settingsValidatingWebhookName)
	require.Contains(t, err.Error(), "time: invalid duration")
}

func settingToUnstructured(setting *managementv3.Setting) (*unstructured.Unstructured, error) {
	object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(setting)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: object}, nil
}

func TestWebhookAuthUserInfoMaxAgeTestSuite(t *testing.T) {
	suite.Run(t, new(WebhookAuthUserInfoMaxAgeTestSuite))
}
