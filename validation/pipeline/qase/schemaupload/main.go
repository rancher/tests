package main

import (
	"flag"
	"os"
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
	schemaPrefix := flag.String("schema-prefix", "", "Schema file prefix to upload, for example hostbusters")
	flag.Parse()

	if *schemaPrefix != "" {
		_ = os.Setenv(qase.SchemaPrefixEnvVar, *schemaPrefix)
	}

	client := qase.SetupQaseClient()

	err := qase.UploadSchemas(client, *basepath)
	if err != nil {
		logrus.Error(err)
	}
}
