package core

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/go-github/github"
	"github.com/ldez/gcg/label"
	"github.com/ldez/gcg/types"
	"golang.org/x/oauth2"
)

const (
	GitHubSearchDateLayout = "2006-01-02T15:04:05Z"
)

func Generate(config *types.Configuration) {
	ctx := context.Background()

	client := newGitHubClient(ctx, config.GitHubToken)

	// Get previous ref date
	commitPreviousRef, _, err := client.Repositories.GetCommit(ctx, config.Owner, config.RepositoryName, config.PreviousRef)
	check(err)

	datePreviousRef := commitPreviousRef.Commit.Committer.Date.Add(1 * time.Second).Format(GitHubSearchDateLayout)

	// Get current ref version date
	commitCurrentRef, _, err := client.Repositories.GetCommit(ctx, config.Owner, config.RepositoryName, config.CurrentRef)
	check(err)

	dateCurrentRef := commitCurrentRef.Commit.Committer.Date.Add(1 * time.Second).Format(GitHubSearchDateLayout)

	// Search PR
	query := fmt.Sprintf("type:pr is:merged repo:%s/%s base:%s merged:%s..%s",
		config.Owner, config.RepositoryName, config.BaseBranch, datePreviousRef, dateCurrentRef)
	if config.Debug {
		log.Println(query)
	}

	searchOptions := &github.SearchOptions{
		Sort:        "created",
		Order:       "asc",
		ListOptions: github.ListOptions{PerPage: 20},
	}

	issues := searchAllIssues(ctx, client, query, searchOptions, config)
	display(config, issues, commitCurrentRef)
}

func searchAllIssues(ctx context.Context, client *github.Client, query string, searchOptions *github.SearchOptions, config *types.Configuration) []github.Issue {
	var allIssues []github.Issue
	for {
		issuesSearchResult, resp, err := client.Search.Issues(ctx, query, searchOptions)
		if err != nil {
			log.Fatal(err)
		}
		for _, issue := range issuesSearchResult.Issues {
			if contains(issue.Labels, config.LabelExclude) {
				if config.Debug {
					log.Println("Exclude:", *issue.Number, *issue.Title)
				}
			} else {
				allIssues = append(allIssues, issue)
			}
		}
		if resp.NextPage == 0 {
			break
		}
		searchOptions.Page = resp.NextPage
	}
	return allIssues
}

func display(config *types.Configuration, issues []github.Issue, commitCurrentRef *github.RepositoryCommit) {
	summary := &types.Summary{
		Owner:          config.Owner,
		RepositoryName: config.RepositoryName,
	}

	for _, issue := range issues {
		if contains(issue.Labels, config.LabelDocumentation) {
			summary.Documentation = makeAndAppendIssueSummary(summary.Documentation, issue, config)
		} else if contains(issue.Labels, config.LabelEnhancement) {
			summary.Enhancement = makeAndAppendIssueSummary(summary.Enhancement, issue, config)
		} else if contains(issue.Labels, config.LabelBug) {
			summary.Bug = makeAndAppendIssueSummary(summary.Bug, issue, config)
		} else {
			summary.Other = makeAndAppendIssueSummary(summary.Other, issue, config)
		}
	}

	summary.CurrentRefDate = commitCurrentRef.Commit.Committer.Date.Format("2006-01-02")
	if len(config.FutureCurrentRefName) == 0 {
		summary.CurrentRefName = config.CurrentRef
	} else {
		summary.CurrentRefName = config.FutureCurrentRefName
	}

	summary.PreviousRefName = config.PreviousRef

	//// TODO Milestone?

	viewTemplate := `{{define "LineTemplate"}}- {{.FilteredLabelNames}}{{.Issue.Title |html}} ([#{{.Issue.Number}}]({{.Issue.URL}}) by [{{.Issue.User.Login}}]({{.Issue.User.URL}})){{end}}
## [{{.CurrentRefName}}](https://github.com/{{.Owner}}/{{.RepositoryName}}/tree/{{.CurrentRefName}}) ({{.CurrentRefDate}})
[All Commits](https://github.com/{{.Owner}}/{{.RepositoryName}}/compare/{{.PreviousRefName}}...{{.CurrentRefName}})

{{if .Enhancement -}}
**Enhancements:**
{{range .Enhancement -}}
{{template "LineTemplate" .}}
{{end}}
{{- end}}
{{if .Bug -}}
**Bug fixes:**
{{range .Bug -}}
{{template "LineTemplate" .}}
{{end}}
{{- end}}
{{if .Documentation -}}
**Documentation:**
{{range .Documentation -}}
{{template "LineTemplate" .}}
{{end}}
{{- end}}
{{if .Other -}}
**Misc:**
{{range .Other -}}
{{template "LineTemplate" .}}
{{end}}
{{- end}}
	`

	tmplt := template.Must(template.New("ChangeLog").Parse(viewTemplate))

	var wr io.Writer
	if config.OutputType == "file" {
		file, err := os.Create(config.FileName)
		defer file.Close()
		check(err)
		wr = file
	} else {
		wr = os.Stdout
	}

	err := tmplt.Execute(wr, summary)
	check(err)
}

func newGitHubClient(ctx context.Context, token string) *github.Client {
	var client *github.Client
	if len(token) == 0 {
		client = github.NewClient(nil)
	} else {
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: token},
		)
		tc := oauth2.NewClient(ctx, ts)
		client = github.NewClient(tc)
	}
	return client
}

func contains(labels []github.Label, str string) bool {
	for _, lbl := range labels {
		if *lbl.Name == str {
			return true
		}
	}
	return false
}

func makeAndAppendIssueSummary(summaries []types.IssueSummary, issue github.Issue, config *types.Configuration) []types.IssueSummary {
	var lbl string

	if config.DisplayLabel {
		labels := label.FilterAndTransform(
			issue.Labels,
			labelFilter(config.DisplayLabelOptions),
			trimAllPrefix(config.DisplayLabelOptions))

		if len(labels) != 0 {
			lbl = fmt.Sprintf("**[%s]** ", strings.Join(labels, ","))
		}
	}

	is := types.IssueSummary{
		FilteredLabelNames: lbl,
		Issue:              issue,
	}
	return append(summaries, is)
}

func trimAllPrefix(options *types.DisplayLabelOptions) label.NameTransform {
	if options != nil {
		return label.TrimAllPrefix(options.TrimmedPrefixes)
	}
	return label.NameIdentity
}

func labelFilter(options *types.DisplayLabelOptions) label.Predicate {

	if options != nil {
		return label.AllMatch(
			label.FilteredBy(label.HasPrefix, options.FilteredPrefixes),
			label.ExcludedBy(label.HasPrefix, options.ExcludedPrefixes),
			label.FilteredBy(label.HasSuffix, options.FilteredSuffixes),
			label.ExcludedBy(label.HasSuffix, options.ExcludedSuffixes))
	}

	return label.All
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
