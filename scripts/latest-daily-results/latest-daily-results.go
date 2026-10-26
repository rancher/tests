package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	qaseactions "github.com/rancher/tests/actions/qase"
	"github.com/sirupsen/logrus"
)

const (
	// dailyRunTitlePrefix is the title of the daily Test Run on Qase that hold the target results.
	dailyRunTitlePrefix = "PIT daily Test Run"
	// project is the Qase project code the daily test runs belong to.
	project = "RANCHERINT"
	// dayFormat is the human readable day label used in the report.
	dateFormat = "Mon Jan 02 15:04:05 MST"
)

func main() {
	if os.Getenv(qaseactions.QaseTokenEnvVar) == "" {
		logrus.Fatalf("%s environment variable is not set", qaseactions.QaseTokenEnvVar)
	}

	qaseService := qaseactions.SetupQaseClient()

	now := time.Now()
	midnightToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	from := midnightToday.AddDate(0, 0, -1) // Get midnight of the previous day to assure that we will grab the run that we want.
	runs, err := qaseService.GetLatestTestRuns(project, dailyRunTitlePrefix, from, 1)
	if err != nil {
		logrus.Fatalf("Error getting latest '%s' run: %v", dailyRunTitlePrefix, err)
	}

	if len(runs) == 0 {
		fmt.Print("No daily run in the past day :face_with_diagonal_mouth:\n")
		return
	}

	dailyRun := runs[0]
	fmt.Print(":loading: *Daily run*\n\n")
	fmt.Printf("Showing test results for daily test run [%s](https://app.qase.io/run/RANCHERINT/dashboard/%d) from %s\n\n", *dailyRun.Title, *dailyRun.Id, dailyRun.StartTime.Get().UTC().Format(dateFormat))
	fmt.Println("Test Run description:")
	scanner := bufio.NewScanner(strings.NewReader(*dailyRun.Description.Get()))

	// Process the description up until a certain point to avoid an overly verbose report.
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Println(line)

		if strings.HasPrefix(line, "Kubernetes version") {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		logrus.Fatalf("Error reading string: %s", err.Error())
	}

	fmt.Print("\n") // Skip one line for a more organized output

	caseTitles, _, err := qaseService.GetFailedTestsForRun(project, int32(*dailyRun.Id))
	if err != nil {
		logrus.Fatalf("Error getting failed tests for run %d: %v", *dailyRun.Id, err)
	}

	if len(caseTitles) == 0 {
		fmt.Println(":check: All tests passed! (ﾉ◕ヮ◕)ﾉ*:･ﾟ✧")
		return
	}

	fmt.Println("Listing failed tests:")
	for _, failedTestTitle := range caseTitles {
		fmt.Println(":x: " + failedTestTitle)
	}
}
