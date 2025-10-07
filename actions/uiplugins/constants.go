package uiplugins

import (
	"github.com/rancher/shepherd/extensions/defaults"
)

const (
    harvesterExtensionsName     = "harvester"
	stackstateExtensionsName	= "observability"
	stackstatePluginName		= "rancher-ui-plugins"
	local						= "local"
)

var (
	timeoutSeconds = int64(defaults.TwoMinuteTimeout)
)