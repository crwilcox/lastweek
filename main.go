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

// Last week provides a report of the work done by the user last week.
// This can be useful for sharing with your coworkers, or just for your own
// notes.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"

	"github.com/google/go-github/v60/github"
	"golang.org/x/oauth2"
)

var (
	stderr = os.Stderr
	stdout = os.Stdout

	userFlag        = flag.String("user", "", "Your GitHub username.")
	githubTokenFlag = flag.String("token", "", "Your GitHub access token.")
	startDateFlag   = flag.String("start_date", "", "The start date in ISO layout. E.g. YYYY-MM-DD")
	endDateFlag     = flag.String("end_date", "", "The end date in ISO layout. E.g. YYYY-MM-DD")
	startOfWeekFlag = flag.String("start_of_week", "Saturday", "The first day of your snippet week")
	weeksBackFlag   = flag.Int("weeks_back", 1, "The number of weeks ago to see snippets for")
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	err := innerMain(ctx)

	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		os.Exit(1)
	}
}

func innerMain(ctx context.Context) error {
	flag.Parse()
	client := http.DefaultClient

	// Parse envvars and flags
	githubToken, err := githubToken()
	if err != nil {
		fmt.Println(
			"$GITHUB_TOKEN or -token flag not set - GitHub may block your " +
				"queries due to rate-limiting " +
				"(https://help.github.com/articles/creating-a-personal-access-token-for-the-command-line/). " +
				"Also note private repository activity will not be reported")
	} else {
		// If a GitHub Personal Access Token was provided, authenticate with it.
		// This will avoid restrictive GitHub rate limits.
		tokenSrc := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: githubToken},
		)
		client = oauth2.NewClient(ctx, tokenSrc)
	}
	ghClient := github.NewClient(client)

	githubUsername, err := username(ctx, ghClient)
	if err != nil {
		return err
	}
	startTime, endTime, err := timerange()
	if err != nil {
		return err
	}

	fmt.Fprintf(stderr, "Pulling contributions from %s to %s...\n",
		startTime.Format(time.RFC3339), endTime.Format(time.RFC3339))

	openedIssues := make(map[string]map[int]*github.Issue)
	closedIssues := make(map[string]map[int]*github.Issue)
	commentedIssues := make(map[string]map[int]*github.Issue)
	openedPullRequests := make(map[string]map[int]*github.PullRequest)
	reviewedPullRequests := make(map[string]map[int]*github.PullRequest)
	closedPullRequests := make(map[string]map[int]*github.PullRequest)

	options := &github.ListOptions{Page: 0}
	for {
		events, resp, err := ghClient.Activity.ListEventsPerformedByUser(ctx, githubUsername, false, options)
		if err != nil {
			return err
		}

		// Process each event within the page
		for _, event := range events {
			if event.CreatedAt.Before(startTime) || event.CreatedAt.After(endTime) {
				continue
			}

			repo := event.Repo
			payload, err := event.ParsePayload()
			if err != nil {
				return fmt.Errorf("failed to parse event payload: %v", err)
			}
			switch p := payload.(type) {
			case *github.IssueCommentEvent:
				issue := p.Issue
				if issue == nil {
					return fmt.Errorf("issue is nil: %v", p)
				}
				switch *p.Action {
				case "created":
					if p.Issue.IsPullRequest() {
						// Pull requests are issues, if a comment is left on a
						// PR that wasn't opened by the user, consider that
						// part of PR review.
						if *p.Issue.User.Login != githubUsername {
							if reviewedPullRequests[*repo.Name] == nil {
								reviewedPullRequests[*repo.Name] = make(map[int]*github.PullRequest)
							}
							s := strings.Split(*repo.Name, "/")
							pull, _, err := ghClient.PullRequests.Get(ctx, s[0], s[1], *issue.Number)
							if err != nil {
								return err
							}
							reviewedPullRequests[*repo.Name][*issue.Number] = pull
						}
					} else {
						if commentedIssues[*repo.Name] == nil {
							commentedIssues[*repo.Name] = make(map[int]*github.Issue)
						}
						commentedIssues[*repo.Name][*issue.Number] = issue
					}
				}
			case *github.IssuesEvent:
				issue := p.Issue
				if issue == nil {
					return fmt.Errorf("issue is nil: %v", p)
				}

				switch *p.Action {
				case "opened":
					if openedIssues[*repo.Name] == nil {
						openedIssues[*repo.Name] = make(map[int]*github.Issue)
					}
					openedIssues[*repo.Name][*issue.Number] = p.Issue
				case "closed":
					if closedIssues[*repo.Name] == nil {
						closedIssues[*repo.Name] = make(map[int]*github.Issue)
					}
					closedIssues[*repo.Name][*issue.Number] = p.Issue
				}
			case *github.PullRequestEvent:
				pullRequest := p.PullRequest
				if pullRequest == nil {
					return fmt.Errorf("pullRequest is nil: %v", p)
				}

				switch *p.Action {
				case "created", "opened", "reopened":
					if openedPullRequests[*repo.Name] == nil {
						openedPullRequests[*repo.Name] = make(map[int]*github.PullRequest)
					}
					openedPullRequests[*repo.Name][*pullRequest.Number] = p.PullRequest
				case "closed": // Heh.
					if closedPullRequests[*repo.Name] == nil {
						closedPullRequests[*repo.Name] = make(map[int]*github.PullRequest)
					}
					closedPullRequests[*repo.Name][*pullRequest.Number] = p.PullRequest
				}
			case *github.PullRequestReviewCommentEvent:
				pullRequest := p.PullRequest
				if pullRequest == nil {
					return fmt.Errorf("pullRequest is nil: %v", p)
				}

				switch *p.Action {
				case "created":
					if reviewedPullRequests[*repo.Name] == nil {
						reviewedPullRequests[*repo.Name] = make(map[int]*github.PullRequest)
					}
					reviewedPullRequests[*repo.Name][*pullRequest.Number] = p.PullRequest
				}
			default:
				// Ignore.
				continue
			}

		}

		// Pages will loop around, if the next page is less, we have already seen it.
		if options.Page == resp.LastPage || resp.NextPage < options.Page {
			break
		}
		options.Page = resp.NextPage
	}

	var w strings.Builder

	if len(openedIssues) > 0 {
		repos := make([]string, 0, len(openedIssues))
		for k := range openedIssues {
			repos = append(repos, k)
		}

		fmt.Fprintf(&w, "### Opened issues\n\n")
		for _, r := range repos {
			formatRepo(&w, r)

			issues := sortIssues(openedIssues[r])
			for _, i := range issues {
				formatIssue(&w, i)
			}
			fmt.Fprintln(&w)
		}
		fmt.Fprintln(&w)
	}

	if len(closedIssues) > 0 {
		repos := make([]string, 0, len(closedIssues))
		for k := range closedIssues {
			repos = append(repos, k)
		}

		fmt.Fprintf(&w, "### Closed issues\n\n")
		for _, r := range repos {
			formatRepo(&w, r)

			issues := sortIssues(closedIssues[r])
			for _, i := range issues {
				formatIssue(&w, i)
			}
			fmt.Fprintln(&w)
		}
		fmt.Fprintln(&w)
	}

	if len(commentedIssues) > 0 {
		repos := make([]string, 0, len(commentedIssues))
		for k := range commentedIssues {
			repos = append(repos, k)
		}

		fmt.Fprintf(&w, "### Commented issues\n\n")
		for _, r := range repos {
			formatRepo(&w, r)

			issues := sortIssues(commentedIssues[r])
			for _, i := range issues {
				formatIssue(&w, i)
			}
			fmt.Fprintln(&w)
		}
		fmt.Fprintln(&w)
	}

	if len(openedPullRequests) > 0 {
		repos := make([]string, 0, len(openedPullRequests))
		for k := range openedPullRequests {
			repos = append(repos, k)
		}

		fmt.Fprintf(&w, "### Pull requests opened\n\n")
		for _, r := range repos {
			formatRepo(&w, r)

			pullRequests := sortPullRequests(openedPullRequests[r])
			for _, i := range pullRequests {
				formatPullRequest(&w, i)
			}
			fmt.Fprintln(&w)
		}
		fmt.Fprintln(&w)
	}

	if len(closedPullRequests) > 0 {
		repos := make([]string, 0, len(closedPullRequests))
		for k := range closedPullRequests {
			repos = append(repos, k)
		}

		fmt.Fprintf(&w, "### Pull requests closed\n\n")
		for _, r := range repos {
			formatRepo(&w, r)

			pullRequests := sortPullRequests(closedPullRequests[r])
			for _, i := range pullRequests {
				formatPullRequest(&w, i)
			}
			fmt.Fprintln(&w)
		}
		fmt.Fprintln(&w)
	}

	if len(reviewedPullRequests) > 0 {
		repos := make([]string, 0, len(reviewedPullRequests))
		for k := range reviewedPullRequests {
			repos = append(repos, k)
		}

		fmt.Fprintf(&w, "### Code reviews\n\n")
		for _, r := range repos {
			formatRepo(&w, r)

			pullRequests := sortPullRequests(reviewedPullRequests[r])
			for _, i := range pullRequests {
				formatPullRequest(&w, i)
			}
			fmt.Fprintln(&w)
		}
		fmt.Fprintln(&w)
	}

	fmt.Println(w.String())

	return nil
}

func formatRepo(w io.Writer, s string) {
	fmt.Fprintf(w, "-   **%s**\n\n", s)
}

func formatIssue(w io.Writer, i *github.Issue) {
	fmt.Fprintf(w, "    -   [%s](%s)\n", *i.Title, *i.HTMLURL)
}

func formatMerged(w io.Writer, i *github.PullRequest) {
	if i.Merged != nil {
		if *i.Merged {
			fmt.Fprintf(w, " [merged]\n")
		} else {
			fmt.Fprintf(w, " [not merged]\n")
		}
	} else {
		fmt.Fprintf(w, "\n")
	}
}

func formatPullRequest(w io.Writer, i *github.PullRequest) {
	fmt.Fprintf(w, "    -   [%s](%s)", *i.Title, *i.HTMLURL)
	formatMerged(w, i)
}

func sortIssues(m map[int]*github.Issue) []*github.Issue {
	issues := make([]*github.Issue, 0, len(m))
	for _, i := range m {
		issues = append(issues, i)
	}
	sort.Slice(issues, func(i, j int) bool {
		return *issues[i].Number < *issues[j].Number
	})
	return issues
}

func sortPullRequests(m map[int]*github.PullRequest) []*github.PullRequest {
	pullRequests := make([]*github.PullRequest, 0, len(m))
	for _, i := range m {
		pullRequests = append(pullRequests, i)
	}
	sort.Slice(pullRequests, func(i, j int) bool {
		return *pullRequests[i].Number < *pullRequests[j].Number
	})
	return pullRequests
}
