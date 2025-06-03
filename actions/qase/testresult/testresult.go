package testresult

// GoTestOutput is the JSON output from gotestsum, this what is used to parse go test results.
type GoTestOutput struct {
	Time    string `json:"Time" yaml:"Time"`
	Action  string `json:"Action" yaml:"Action"`
	Package string `json:"Package" yaml:"Package"`
	Test    string `json:"Test" yaml:"Test"`
	Output  string `json:"Output" yaml:"Output"`
	Elapsed string `json:"Elapsed" yaml:"Elapsed"`
}

// GoTestResult is the struct for holding test results
type GoTestResult struct {
	Name       string
	Package    string
	TestSuite  []string
	Status     string
	StackTrace string
	Elapsed    string
}
