package appco

import (
	"flag"
	"os"
)

var (
	// AppCoUsername is the username for authenticating with the Application Collection registry (dp.apps.rancher.io).
	AppCoUsername *string = flag.String("APPCO_USERNAME", os.Getenv("APPCO_USERNAME"), "Application Collection username for dp.apps.rancher.io")
	// AppCoAccessToken is the access token for authenticating with the Application Collection registry (dp.apps.rancher.io).
	AppCoAccessToken *string = flag.String("APPCO_ACCESS_TOKEN", os.Getenv("APPCO_ACCESS_TOKEN"), "Application Collection access token for dp.apps.rancher.io")
)
