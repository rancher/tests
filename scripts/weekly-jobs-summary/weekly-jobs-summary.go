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
	// weeklyRunTitlePrefix is the title of the weekly Test Run on Qase that hold the target results.
	weeklyRunTitlePrefix = "PIT weekly Test Run"
	// dailyRunTitlePrefix is the title of the daily Test Run on Qase that hold the target results.
	dailyRunTitlePrefix = "PIT daily Test Run"
	// project is the Qase project code the daily PIT runs belong to.
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
	from := midnightToday.AddDate(0, 0, -7) // Get midnight of the same day on the previous week to assure that we will grab the runs that we want.

	weeklyRunSlice, err := qaseService.GetLatestTestRuns(project, weeklyRunTitlePrefix, from, 3) // There are three weekly runs for different rancher versions.
	if err != nil {
		logrus.Fatalf("error getting '%s' runs for the past week: %v", weeklyRunTitlePrefix, err)
	}

	fmt.Print(":loading: *Weekly runs*\n\n")

	if len(weeklyRunSlice) < 1 {
		fmt.Print("No completed weekly run this week :face_with_diagonal_mouth:\n\n")
	} else {
		for _, weeklyRun := range weeklyRunSlice {
			fmt.Printf("Showing test results for test run [%s](https://app.qase.io/run/RANCHERINT/dashboard/%d) from %s\n\n", *weeklyRun.Title, *weeklyRun.Id, weeklyRun.StartTime.Get().UTC().Format(dateFormat))
			fmt.Println("Test Run description:")
			scanner := bufio.NewScanner(strings.NewReader(strings.ReplaceAll(*weeklyRun.Description.Get(), "<br/>", "\n"))) // Replace <br/> for actual spaces for proper scanning.

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

			caseTitles, _, err := qaseService.GetFailedTestsForRun(project, int32(*weeklyRun.Id))
			if err != nil {
				logrus.Fatalf("Error getting failed tests for run %d: %v", *weeklyRun.Id, err)
			}

			if len(caseTitles) == 0 {
				fmt.Println(":check: All tests passed! (ﾉ◕ヮ◕)ﾉ*:･ﾟ✧")
				continue
			}

			fmt.Println("Listing failed tests:")
			for _, failedTestTitle := range caseTitles {
				fmt.Println(":x: " + failedTestTitle)
			}

			fmt.Print("\n") // Skip one line for a more organized output
		}
	}

	dailyRuns, err := qaseService.GetLatestTestRuns(project, dailyRunTitlePrefix, from, 7)
	if err != nil {
		logrus.Fatalf("error getting '%s' runs for the past week: %v", dailyRunTitlePrefix, err)
	}

	if len(dailyRuns) < 1 {
		fmt.Print("No daily run this week :face_with_diagonal_mouth:\n\n")
	} else {
		// testDays maps a failed test title to the set of days it failed on.
		testDays := map[string][]string{}

		for _, run := range dailyRuns {
			failedTestsTitles, _, err := qaseService.GetFailedTestsForRun(project, int32(*run.Id))
			if err != nil {
				logrus.Warningf("error getting failed tests for run %d: %v", *run.Id, err)
				continue
			}

			dayString := fmt.Sprintf("[%s](https://app.qase.io/run/RANCHERINT/dashboard/%d)", run.StartTime.Get().UTC().Format("Monday"), *run.Id)

			for _, testTitle := range failedTestsTitles {
				if _, ok := testDays[testTitle]; !ok {
					testDays[testTitle] = []string{}
				}

				testDays[testTitle] = append(testDays[testTitle], dayString)
			}
		}

		fmt.Print(":loading: *Daily runs*\n\n")

		if len(testDays) == 0 {
			fmt.Print("No failing tests this week look at that :partying-face:\n\n")
			return
		}

		fmt.Print("The following tests failed at some point on the week:\n\n")

		for title, dayList := range testDays {
			fmt.Println("The test _" + title + "_ failed on: " + strings.Join(dayList, " ,"))
		}
	}
}
