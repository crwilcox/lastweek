// Copyright 2022 lastweek authors (see AUTHORS file)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/go-github/v42/github"
)

// githubToken returns the github token to use. Priority is given to the
// cmd line flag, then env var.
func githubToken() (string, error) {
	if *githubTokenFlag != "" {
		fmt.Println("Using GitHub personal access token provided via flag.")
		return *githubTokenFlag, nil
	}

	githubToken := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if githubToken != "" {
		fmt.Println("Using GitHub personal access token found in $GITHUB_TOKEN.")
		return githubToken, nil

	}

	return "", fmt.Errorf("github access token not provided via -token flag or $GITHUB_TOKEN")
}

// username returns the github username to use. Priority is given to the
// cmd line flag, then env var, lastly, autodiscovery of the username is
// attempted from the provided GitHub access token.
func username(ctx context.Context, ghClient *github.Client) (string, error) {
	if *userFlag != "" {
		fmt.Printf("User identified as %s via flag\n", *userFlag)
		return *userFlag, nil
	}

	envvarUsername := strings.TrimSpace(os.Getenv("GITHUB_USERNAME"))
	if envvarUsername != "" {
		fmt.Printf("User identified as %s via environment variable\n", envvarUsername)
		return envvarUsername, nil
	}

	// If a GitHub Personal Access Token was provided, we can identify the user login
	fmt.Println(
		"User not specified via flag or environment variable,",
		"attempting to detect from access token.")

	user, _, err := ghClient.Users.Get(ctx, "")
	if err != nil || user == nil {
		return "", fmt.Errorf("failed to identify user")
	}

	fmt.Printf("User identified as %s\n", *user.Login)
	return *user.Login, nil
}

// timerange determines the start and end times from the different timerange
// flags: start_date, end_date, start_of_week, weeks_back
func timerange() (startTime, endTime time.Time, err error) {
	// Determine start and end times
	if *startDateFlag != "" && *endDateFlag != "" {
		if startTime, err = time.Parse("2006-01-02", *startDateFlag); err != nil {
			return
		}
		if endTime, err = time.Parse("2006-01-02", *endDateFlag); err != nil {
			return
		}
	} else {
		weekdays := map[string]time.Weekday{
			"SUNDAY":    time.Sunday,
			"MONDAY":    time.Monday,
			"TUESDAY":   time.Tuesday,
			"WEDNESDAY": time.Wednesday,
			"THURSDAY":  time.Thursday,
			"FRIDAY":    time.Friday,
			"SATURDAY":  time.Saturday,
		}
		startOfWeek, ok := weekdays[strings.ToUpper(*startOfWeekFlag)]
		if !ok {
			return startTime, endTime, fmt.Errorf("invalid value for --start_of_week: %q", *startOfWeekFlag)
		}

		startTime, endTime, err = weekBounds(time.Now().UTC(), *weeksBackFlag, startOfWeek)
		if err != nil {
			return
		}
	}
	return
}

// weekBounds returns the start and end of a week, relative to a given time.
// It finds the week that was a specified number of weeks ago (0 being the current week), then
// calculates the bounds of that week based on a specified day being the first day of the week.
func weekBounds(t time.Time, weeksBack int, firstDay time.Weekday) (time.Time, time.Time, error) {
	if firstDay < time.Sunday || firstDay > time.Saturday {
		return time.Time{}, time.Time{}, fmt.Errorf("not a valid day: %v", firstDay)
	}

	startTime := t.Add(-time.Hour * 24 * 7 * time.Duration(weeksBack))
	for startTime.Weekday() != firstDay {
		startTime = startTime.Add(-time.Hour * 24)
	}

	_, timezoneOffset := startTime.Zone()
	startTime = startTime.Truncate(time.Hour * 24).Add(-time.Duration(timezoneOffset) * time.Second)
	endTime := startTime.Add(time.Hour * 24 * 7)
	return startTime, endTime, nil
}
