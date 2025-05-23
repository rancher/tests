package appco

import (
	"os"
)

var (
	AppCoUsername    string = os.Getenv("APPCO_USERNAME")
	AppCoAccessToken string = os.Getenv("APPCO_ACCESS_TOKEN")
)
