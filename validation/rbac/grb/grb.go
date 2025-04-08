package grb

import (
	"fmt"
	"time"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/users"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/rbac"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	deploymentNamespace                                       = "cattle-system"
	deploymentName                                            = "rancher"
	deploymentEnvVarName                                      = "CATTLE_RESYNC_DEFAULT"
	dummyFinalizer                                            = "dummy.example.com"
	falseConditionStatus               metav1.ConditionStatus = "False"
	trueConditionStatus                metav1.ConditionStatus = "True"
	errorConditionStatus                                      = "Error"
	failedToGetGlobalRoleReason                               = "FailedToGetGlobalRole"
	completedSummary                                          = "Completed"
	clusterPermissionsReconciled                              = "ClusterPermissionsReconciled"
	globalRoleBindingReconciled                               = "GlobalRoleBindingReconciled"
	namespacedRoleBindingReconciled                           = "NamespacedRoleBindingReconciled"
	fleetWorkspacePermissionReconciled                        = "FleetWorkspacePermissionReconciled"
	clusterAdminRoleExists                                    = "ClusterAdminRoleExists"
	upnString                                                 = "testuser1"
	principalDisplayNameAnnotation                            = "auth.cattle.io/principal-display-name"
	testPrincipalDisplayName                                  = "testPrincipalDisplayName"
	testGroupPrincipalName                                    = "testGroupPrincipalName"
)

var (
	customGlobalRole = v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"management.cattle.io"},
				Resources: []string{"clusters"},
				Verbs:     []string{"*"},
			},
		},
	}

	customGlobalRoleBinding = v3.GlobalRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "",
			Annotations: map[string]string{},
		},
		GlobalRoleName:    "",
		UserPrincipalName: upnString,
	}
)

func createGlobalRoleAndUser(client *rancher.Client) (*v3.GlobalRole, *management.User, error) {
	customGlobalRole.Name = namegen.AppendRandomString("testgr")
	createdGlobalRole, err := client.WranglerContext.Mgmt.GlobalRole().Create(&customGlobalRole)
	if err != nil {
		return nil, nil, err
	}

	createdGlobalRole, err = rbac.GetGlobalRoleByName(client, createdGlobalRole.Name)
	if err != nil {
		return nil, nil, err
	}

	createdUser, err := users.CreateUserWithRole(client, users.UserConfig(), rbac.StandardUser.String(), customGlobalRole.Name)
	if err != nil {
		return nil, nil, err
	}

	return createdGlobalRole, createdUser, err
}

func verifyGlobalRoleBindingStatusField(grb *v3.GlobalRoleBinding, isAdminGlobalRole bool) error {
	status := grb.Status

	_, err := time.Parse(time.RFC3339, status.LastUpdateTime)
	if err != nil {
		return fmt.Errorf("lastUpdateTime is invalid: %w", err)
	}

	requiredLocalConditions := []string{
		clusterPermissionsReconciled,
		globalRoleBindingReconciled,
		namespacedRoleBindingReconciled,
	}
	for _, condition := range status.LocalConditions {
		for _, reqType := range requiredLocalConditions {
			if condition.Type == reqType {
				if condition.Status != trueConditionStatus {
					return fmt.Errorf("%s condition is not True. Actual status: %s", reqType, condition.Status)
				}

				if condition.LastTransitionTime.IsZero() {
					return fmt.Errorf("%s lastTransitionTime is not set or invalid", reqType)
				}

				if condition.Message != "" {
					return fmt.Errorf("%s message should be empty. Actual message: %s", reqType, condition.Message)
				}

				if condition.Reason != condition.Type {
					return fmt.Errorf("Expected: %s, Actual: %s", condition.Type, condition.Reason)
				}
			}
		}
	}

	if status.ObservedGenerationLocal != 1 {
		return fmt.Errorf("observedGenerationLocal is not 1, found: %d", status.ObservedGenerationLocal)
	}

	if status.Summary != completedSummary || status.SummaryLocal != completedSummary {
		return fmt.Errorf("summary or summaryLocal is not 'Completed'")
	}

	if isAdminGlobalRole {
		if status.RemoteConditions != nil {
			for _, condition := range status.RemoteConditions {
				if condition.Type == clusterAdminRoleExists && condition.Status != trueConditionStatus {
					return fmt.Errorf("clusterAdminRoleExists condition is not True. Actual status: %s", condition.Status)
				}

				if condition.LastTransitionTime.IsZero() {
					return fmt.Errorf("%s lastTransitionTime is not set or invalid", clusterAdminRoleExists)
				}

				if condition.Message != "" {
					return fmt.Errorf("%s message should be empty. Actual message: %s", clusterAdminRoleExists, condition.Message)
				}

				if condition.Reason != condition.Type {
					return fmt.Errorf("Expected: %s, Actual: %s", condition.Type, condition.Reason)
				}
			}
		}

		if status.ObservedGenerationRemote != 1 {
			return fmt.Errorf("observedGenerationRemote is not 1, found: %d", status.ObservedGenerationRemote)
		}

		if status.SummaryRemote != completedSummary {
			return fmt.Errorf("summaryRemote is not 'Completed'")
		}
	}

	return nil
}

func verifyUserByPrincipalIDExists(client *rancher.Client, principalID string) error {
	userList, err := client.WranglerContext.Mgmt.User().List(metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("Failed to list users %w", err)
	}

	for _, user := range userList.Items {
		for _, principalId := range user.PrincipalIDs {
			if principalId == principalID {
				return nil
			}
		}
	}
	return nil
}
