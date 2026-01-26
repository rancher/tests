# Copilot Instructions for Test Development with Rancher/Suse Products

## Overview

This document provides guidelines for GitHub Copilot to assist with test development in the Rancher/tests test suite. The repository contains one main test framework in Golang
This framework utilizes actions, interoperability, and extensions, a collection of packages around various APIs including but not limited to Steve, Wrangler, PublicAPI.
Exentsions are located in the [shepherd repo](https://github.com/rancher/shepherd)

## Actions Vs. Extensions

The following qualify a function to be an extension:

* Must be either an api call not natively captured by the client OR specific CRUD / wait on resource
  * i.e. download ssh keys
  * tokens package
  * should not only directly convert -> this would make it an action

Disqualifiers for extensions (If any one or more are true, function is an action):

* if there's a custom config needed, in any part
* if it is a validation of any sort (Waits are okay)
* if function is a direct conversion of a resource
  * more on this, if the function is purely just calling a native call + catching the error, this should not be an extension. A conversion would be more complex; see an example like token package
* how re-usable is the code? Shepherd code should be highly re-usable

### 1. Adding Test Cases in Validation Folder

#### Location

* Directory: validation/
* Test fixtures: tests/harvester_e2e_tests/fixtures/

#### Directory Structure

* new folder per test feature / area
* tests live in folders, whose filename always ends in `_test.go`

#### Rules for function creation

* helper functions specific to a test live in a seprate file, without the `_test` filename ending
* functions that can be generalized should be, then placed in a corresponding folder in the actions/ directory
* any functions that match the extensions definition should have a PR opened to the [shepherd repo](https://github.com/rancher/shepherd) repo in the appropriate directory

#### Test Structure

##### Tags

see ../TAG_GUIDE.md for how to add tags to tests

##### Guidelines

* always validate in a separate function
* unless the requested test has `pit` in its tag name, only extensions or actions helpers should be used. In other words, only `pit` tests can use external APIs that are not explicitly rancher
* the featurename should only be in the test's SuiteName, not any of the individual tests
* there should be at least 2 tests per test file when possible:
  * one of which will always be contain `Dynamic` substring, which will depend on user input from "github.com/rancher/shepherd/pkg/config", wrapped in an action. See actions/fleet/fleet.go for an example
  * one of which will always be as static as possible, where no input is needed from the config

##### Example Test

```go
//go:build validation || sanity || pit.daily || pit.harvester.daily

package featurename

import (
    "fmt"
    "net/url"
    "strings"
    "testing"
    // any additional imports from other repos are added here. i.e. 
    "github.com/rancher/featurename/pkg/apis/featurename.cattle.io/v1alpha1"

    provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
    "github.com/rancher/shepherd/clients/rancher"
    steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
    extensionscluster "github.com/rancher/shepherd/extensions/clusters"
    extensions_featurename "github.com/rancher/shepherd/extensions/featurename"
    "github.com/rancher/shepherd/extensions/workloads/pods"
    "github.com/rancher/shepherd/pkg/config"
    "github.com/rancher/shepherd/pkg/namegenerator"
    "github.com/rancher/shepherd/pkg/session"
    projectsapi "github.com/rancher/tests/actions/projects"
    "github.com/rancher/tests/actions/provisioninginput"
    "github.com/rancher/tests/interoperability/featurename"
    "github.com/sirupsen/logrus"
    "github.com/stretchr/testify/require"
    "github.com/stretchr/testify/suite"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type FeatureNamePublicRepoTestSuite struct {
    suite.Suite
    client    *rancher.Client
    session   *session.Session
    clusterID string
}

func (f *FeatureNamePublicRepoTestSuite) TearDownSuite() {
    f.session.Cleanup()
}

// Anything required that is not explicitly part of the test should be included in setup. Below is purely an example where the test depends on an existing rancher kubernetes cluster (a common dependency in tests).
func (f *FeatureNamePublicRepoTestSuite) SetupSuite() {
    f.session = session.NewSession()

    client, err := rancher.NewClient("", f.session)
    require.NoError(f.T(), err)

    f.client = client

    userConfig := new(provisioninginput.Config)
    config.LoadConfig(provisioninginput.ConfigurationFileKey, userConfig)

    clusterObject, _, _ := extensionscluster.GetProvisioningClusterByName(f.client, f.client.RancherConfig.ClusterName, featurename.Namespace)
    if clusterObject != nil {
        status := &provv1.ClusterStatus{}
        err := steveV1.ConvertToK8sType(clusterObject.Status, status)
        require.NoError(f.T(), err)

        f.clusterID = status.ClusterName
    } else {
        f.clusterID, err = extensionscluster.GetClusterIDByName(f.client, f.client.RancherConfig.ClusterName)
        require.NoError(f.T(), err)
    }
    // always validate after getting/creating a resource, even in setup
    podErrors := pods.StatusPods(f.client, f.clusterID)
    require.Empty(f.T(), podErrors)
}

func (f *FeatureNamePublicRepoTestSuite) TestGitRepoDeployment() {
    defer f.session.Cleanup()

    featurenameVersion, err := featurename.GetDeploymentVersion(f.client, featurename.    FeatureNameControllerName, featurename.LocalName)
    require.NoError(f.T(), err)

    urlQuery, err := url.ParseQuery(fmt.Sprintf("labelSelector=%s=%s", "cattle.io/os", "windows"))
    require.NoError(f.T(), err)

    steveClient, err := f.client.Steve.ProxyDownstream(f.clusterID)
    require.NoError(f.T(), err)

    winsNodeList, err := steveClient.SteveType("node").List(urlQuery)
    require.NoError(f.T(), err)

    if len(winsNodeList.Data) > 0 {

        urlQuery, err = url.ParseQuery(fmt.Sprintf("labelSelector=%s=%s", "kubernetes.io/os", "linux"))
        require.NoError(f.T(), err)

        linuxNodeList, err := steveClient.SteveType("node").List(urlQuery)
        require.NoError(f.T(), err)

        if len(winsNodeList.Data) < len(linuxNodeList.Data) {
            featurenameVersion += " windows"
        }
    }

    _, namespace, err := projectsapi.CreateProjectAndNamespace(f.client, f.clusterID)
    require.NoError(f.T(), err)

    f.Run("featurename "+featurenameVersion, func() {
        featurenameGitRepo := v1alpha1.GitRepo{
            ObjectMeta: metav1.ObjectMeta{
                Name:  featurename.    FeatureNameMetaName + namegenerator.RandStringLower(5),
                Namespace: featurename.Namespace,
            },
            Spec: v1alpha1.GitRepoSpec{
                Repo:        featurename.ExampleRepo,
                Branch:      featurename.BranchName,
                Paths:           []string{featurename.GitRepoPathLinux},
                TargetNamespace: namespace.Name,
                CorrectDrift:    &v1alpha1.CorrectDrift{},
                ImageScanCommit: &v1alpha1.CommitSpec{AuthorName: "", AuthorEmail: ""},
                Targets:         []v1alpha1.GitTarget{{ClusterName: f.client.RancherConfig.ClusterName}},
            },
        }

        if strings.Contains(featurenameVersion, "windows") {
            featurenameGitRepo.Spec.Paths = []string{featurename.GitRepoPathWindows}
        }

        f.client, err = f.client.ReLogin()
        require.NoError(f.T(), err)

        logrus.Info("Deploying public featurename gitRepo")
        gitRepoObject, err := extensionsfeaturename.Create    FeatureNameGitRepo(f.client, &featurenameGitRepo)
        require.NoError(f.T(), err)

        err = featurename.VerifyGitRepo(f.client, gitRepoObject.ID, f.clusterID, featurename.Namespace+"/"+f.client.RancherConfig.ClusterName)
        require.NoError(f.T(), err)

    })
}

func (f *    FeatureNamePublicRepoTestSuite) TestDynamicGitRepoDeployment() {
    testSession := session.NewSession()
    defer testSession.Cleanup()
    client, err := f.client.WithSession(testSession)
    require.NoError(f.T(), err)

    dynamicGitRepo := featurename.GitRepoConfig()
    require.NotNil(f.T(), dynamicGitRepo)

    if dynamicGitRepo.Spec.Repo == "" {
        f.T().Skip("no dynamic repo specified")
    }

    if len(dynamicGitRepo.Spec.Targets) < 1 {
        dynamicGitRepo.Spec.Targets = []v1alpha1.GitTarget{
            {
                ClusterName: client.RancherConfig.ClusterName,
            },
        }
    }

    featurenameVersion, err := featurename.GetDeploymentVersion(client, featurename.    FeatureNameControllerName, featurename.LocalName)
    require.NoError(f.T(), err)

    f.Run("featurename "+featurenameVersion, func() {
        client, err = client.ReLogin()
        require.NoError(f.T(), err)

        logrus.Info("Deploying dynamic gitRepo: ", dynamicGitRepo.Spec)

        gitRepoObject, err := extensionsfeaturename.Create    FeatureNameGitRepo(client, dynamicGitRepo)
        require.NoError(f.T(), err)

        // expects dynamicGitRepo.GitRepoSpec.Targets to include RancherConfig.ClusterName
        err = featurename.VerifyGitRepo(client, gitRepoObject.ID, f.clusterID, featurename.Namespace+"/"+client.RancherConfig.ClusterName)
        require.NoError(f.T(), err)
    })
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func Test    FeatureNamePublicRepoTestSuite(t *testing.T) {
    suite.Run(t, new(    FeatureNamePublicRepoTestSuite))
}
```

##### Code Quality

* use golangci-lint for formatting, with ../.golangci.yaml
* Add comments for complex logic

##### Determine if issue is with how the test was written, or aa real bug

* TBD
