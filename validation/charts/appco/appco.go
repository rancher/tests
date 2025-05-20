package appco

import "os"

var (
	AppCoUsername    = os.Getenv("APPCO_USERNAME")
	AppCoAccessToken = os.Getenv("APPCO_ACCESS_TOKEN")
)
