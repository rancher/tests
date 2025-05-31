package appco

import (
	"flag"
	"os"
)

var (
	AppCoUsername    *string = flag.String("APPCO_USERNAME", os.Getenv("APPCO_USERNAME"), "")
	AppCoAccessToken *string = flag.String("APPCO_ACCESS_TOKEN", os.Getenv("APPCO_ACCESS_TOKEN"), "")
)
