package machinepools

import (
	"os"
	"path"
	"regexp"
	"strings"
)

const (
	ciJobTag          = "ci_job"
	ciBuildTag        = "ci_build"
	maxTagValueLength = 256
)

var invalidTagChars = regexp.MustCompile(`[^\p{L}\p{N} _.:/=+\-@]`)

func sanitizeTagValue(value string) string {
	value = invalidTagChars.ReplaceAllString(strings.TrimSpace(value), "-")
	if runes := []rune(value); len(runes) > maxTagValueLength {
		value = string(runes[:maxTagValueLength])
	}

	return value
}

func ciMachineConfigTags() string {
	var job, build string

	if os.Getenv("GITHUB_ACTIONS") == "true" {
		workflow := os.Getenv("GITHUB_WORKFLOW")
		if ref := os.Getenv("GITHUB_WORKFLOW_REF"); ref != "" {
			workflow = path.Base(strings.SplitN(ref, "@", 2)[0])
		}

		job = workflow
		if repo := os.Getenv("GITHUB_REPOSITORY"); repo != "" && workflow != "" {
			job = repo + "/" + workflow
		}

		build = os.Getenv("GITHUB_RUN_ID")
	} else {
		job = os.Getenv("JOB_NAME")
		build = os.Getenv("BUILD_NUMBER")
	}

	var pairs []string
	if build = sanitizeTagValue(build); build != "" {
		pairs = append(pairs, ciBuildTag, build)
	}

	if job = sanitizeTagValue(job); job != "" {
		pairs = append(pairs, ciJobTag, job)
	}

	return strings.Join(pairs, ",")
}

func mergeMachineConfigTags(tags ...string) string {
	var nonEmpty []string
	for _, tag := range tags {
		if tag = strings.Trim(strings.TrimSpace(tag), ","); tag != "" {
			nonEmpty = append(nonEmpty, tag)
		}
	}

	return strings.Join(nonEmpty, ",")
}
