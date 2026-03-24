package main

import (
	"flag"
	"path/filepath"
	"runtime"

	"github.com/rancher/tests/actions/qase"
	"github.com/sirupsen/logrus"
)

var (
	_, callerFilePath, _, _ = runtime.Caller(0)
	defaultBasepath         = filepath.Join(filepath.Dir(callerFilePath), "..", "..", "..", "..")
)

func main() {
	basepath := flag.String("basepath", defaultBasepath, "Base path for schema upload")
	flag.Parse()

	client := qase.SetupQaseClient()

	err := qase.UploadSchemas(client, *basepath)
	if err != nil {
		logrus.Error(err)
	}
}
