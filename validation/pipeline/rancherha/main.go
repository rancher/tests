package main

import (
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/pipeline"
	"github.com/sirupsen/logrus"
)

func main() {
	rancherConfig := new(rancher.Config)
	config.LoadConfig(rancher.ConfigurationFileKey, rancherConfig)
	passwword := rancherConfig.AdminPassword

	token, err := pipeline.CreateAdminToken(passwword, rancherConfig)
	if err != nil {
		logrus.Errorf("error creating the admin token: %v", err)
	}

	rancherConfig.AdminToken = token
	config.UpdateConfig(rancher.ConfigurationFileKey, rancherConfig)
	rancherSession := session.NewSession()
	client, err := rancher.NewClient(rancherConfig.AdminToken, rancherSession)
	if err != nil {
		logrus.Errorf("error creating the rancher client: %v", err)
	}

	err = pipeline.PostRancherInstall(client, rancherConfig.AdminPassword)
	if err != nil {
		logrus.Errorf("error during post rancher install: %v", err)
	}
}
