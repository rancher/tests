package qase

const (
	AutomationSuiteID       = int32(554)
	AutomationTestNameID    = int64(15)
	QaseTokenEnvVar         = "QASE_AUTOMATION_TOKEN"
	RancherManagerProjectID = "RM"
	RecurringRunID          = 1
	RunSourceID             = 16
	TestSourceID            = 14
	TestRunEnvVar           = "QASE_TEST_RUN_ID"
	ProjectIDEnvVar         = "QASE_PROJECT_ID"
	TestRunNameEnvVar       = "TEST_RUN_NAME"
	TestPackagePaths        = "TEST_PACKAGE_PATHS"
	BuildUrl                = "BUILD_URL"
	TestSource              = "GoValidation"
	MultiSubTestPattern     = `(\w+/\w+/\w+){1,}`
	SubtestPattern          = `(\w+/\w+){1,1}`
	TestResultsJSON         = "results.json"
	PassStatus              = "pass"
	FailStatus              = "fail"
	SkipStatus              = "skip"
	TestRunCompleteEnvVar   = "QASE_TEST_RUN_COMPLETE"
)
