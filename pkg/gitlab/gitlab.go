// Copyright 2019 FUSAKLA Martin Chodúr
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gitlab

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"text/template"
	"time"

	"github.com/fusakla/prometheus-gitlab-notifier/pkg/alertmanager"
	"github.com/fusakla/prometheus-gitlab-notifier/pkg/metrics"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/xanzy/go-gitlab"
)

// New creates new Gitlab instance configured to work with specified gitlab instance, project and with given authentication.
func New(logger log.Logger, url string, token string, projectId int, issueTemplate *template.Template, issueLabels *[]string, dynamicIssueLabels *[]string, groupInterval *time.Duration) (*Gitlab, error) {
	cli := gitlab.NewClient(nil, token)
	if err := cli.SetBaseURL(url); err != nil {
		level.Error(logger).Log("msg", "invalid Gitlab URL", "url", url, "err", "err")
		return nil, err
	}
	g := &Gitlab{
		client:             cli,
		projectId:          projectId,
		issueTemplate:      issueTemplate,
		issueLabels:        issueLabels,
		dynamicIssueLabels: dynamicIssueLabels,
		groupInterval:      groupInterval,
		logger:             logger,
	}
	if err := g.ping(); err != nil {
		level.Error(logger).Log("msg", "cannot reach the Gitlab", "url", url, "err", "err")
		return nil, err
	}
	return g, nil
}

// Gitlab holds configured Gitlab client and provides API for creating templated issue from the Webhook.
type Gitlab struct {
	client             *gitlab.Client
	projectId          int
	issueTemplate      *template.Template
	issueLabels        *[]string
	dynamicIssueLabels *[]string
	groupInterval      *time.Duration
	logger             log.Logger
}

func (g *Gitlab) formatGitlabScopedLabel(key string, value string) string {
	return fmt.Sprintf("%s::%s", key, value)
}

func (g *Gitlab) extractDynamicLabels(msg *alertmanager.Webhook) []string {
	var labelsMap = map[string]string{}
	for _, a := range msg.Alerts {
		for k, v := range a.Labels {
			for _, l := range *g.dynamicIssueLabels {
				if k == l {
					labelsMap[k] = v
				}
			}
		}
	}
	var resLabels []string
	for k, v := range labelsMap {
		resLabels = append(resLabels, g.formatGitlabScopedLabel(k, v))
	}
	return resLabels
}

func (g *Gitlab) extractGroupingLabels(msg *alertmanager.Webhook) []string {
	var resLabels []string
	for k, v := range msg.GroupLabels {
		resLabels = append(resLabels, g.formatGitlabScopedLabel(k, v))
	}
	return resLabels
}

func (g *Gitlab) renderIssueTemplate(msg *alertmanager.Webhook) (*bytes.Buffer, error) {
	var issueText bytes.Buffer
	// Try to template the issue text template with the alert data.
	if err := g.issueTemplate.Execute(&issueText, msg.Data); err != nil {
		// As a fallback we try to add raw JSON of the alert to the issue text so we don't miss an alert just because of template error.
		metrics.ReportError("IssueTemplateError", "")
		level.Error(g.logger).Log("msg", "failed to template issue text, using pure JSON instead", "err", err)
		w := bufio.NewWriter(&issueText)
		if err := json.NewEncoder(w).Encode(msg); err != nil {
			// If even JSON marshalling fails we return error
			metrics.ReportError("JSONMarshalError", "")
			level.Error(g.logger).Log("msg", "failed to marshall alert to JSON", "err", err)
			return nil, err
		}
	}
	return &issueText, nil
}

func (g *Gitlab) getOpenIssuesSince(groupingLabels []string, sinceTime time.Time) ([]*gitlab.Issue, error) {
	openState := "opened"
	scope := "created_by_me"
	orderBy := "created_at"
	listOpts := gitlab.ListIssuesOptions{
		Labels:       groupingLabels,
		CreatedAfter: &sinceTime,
		State:        &openState,
		Scope:        &scope,
		OrderBy:      &orderBy,
	}
	issues, response, err := g.client.Issues.ListIssues(&listOpts)
	if err != nil {
		metrics.ReportError("ListGitlabIssuesError", "gitlab")
		level.Error(g.logger).Log("msg", "failed to list gitlab issues with", "opts", listOpts, "response", response, "err", err)
		return []*gitlab.Issue{}, err
	}
	return issues, nil
}

func (g *Gitlab) getTimeBefore(before *time.Duration) time.Time {
	return time.Now().Local().Add(-*before)
}

func (g *Gitlab) createGitlabIssue(msg *alertmanager.Webhook, groupingLabels []string, issueText *bytes.Buffer) error {
	// Collect all new issue labels
	labels := *g.issueLabels
	labels = append(labels, groupingLabels...)
	labels = append(labels, g.extractDynamicLabels(msg)...)
	options := &gitlab.CreateIssueOptions{
		Title:       gitlab.String(fmt.Sprintf("Firing alert `%s`", msg.CommonLabels["alertname"])),
		Description: gitlab.String(issueText.String()),
		Labels:      labels,
	}

	createdIssue, response, err := g.client.Issues.CreateIssue(g.projectId, options)
	if err != nil {
		metrics.ReportError("FailedToCreateGitlabIssue", "gitlab")
		level.Error(g.logger).Log("msg", "failed to create gitlab issue", "err", err, "response", response)
		return err
	}
	level.Info(g.logger).Log("msg", "created issue in gitlab", "gitlab_issue_id", createdIssue.IID, "alert_grouping_key", msg.GroupKey)
	return nil
}

func (g *Gitlab) increaseAppendLabel(labels []string) []string {
	// Every updated issue has special label containing number of updates
	appendLabelRegex := regexp.MustCompile(`(appended-alerts)::(\d+)`)
	alreadyAppended := false
	var newLabels []string
	for _, l := range labels {
		// Check if the label is the special one
		matched := appendLabelRegex.FindStringSubmatch(l)
		if len(matched) == 3 {
			alreadyAppended = true
			// Convert it to number if possible otherwise leave the old one as is
			count, err := strconv.Atoi(matched[2])
			if err != nil {
				level.Error(g.logger).Log("msg", "failed to parse gitlab issue label `appended-alerts`, leaving it unmodified", "label_value", l, "err", err)
				newLabels = append(newLabels, l)
				continue
			}
			// Increase the number of appends and add override the old label with it
			newLabels = append(newLabels, g.formatGitlabScopedLabel(matched[1], strconv.Itoa(count+1)))
			continue
		}
		newLabels = append(newLabels, l)
	}
	if !alreadyAppended {
		newLabels = append(newLabels, g.formatGitlabScopedLabel("appended-alerts", "1"))
	}
	return newLabels
}

func (g *Gitlab) updateGitlabIssue(issue *gitlab.Issue, issueText *bytes.Buffer) error {
	newLabels := g.increaseAppendLabel(issue.Labels)
	options := &gitlab.UpdateIssueOptions{
		// Concat original description with the new rendered template separated by `Appended on <date>` statement
		Description: gitlab.String(fmt.Sprintf("%s\n\n&nbsp;\n\n&nbsp;\n\n&nbsp;\n\n_Appended on `%s`_\n%s", issue.Description, time.Now().Local(), issueText.String())),
		Labels:      newLabels,
	}
	issue, response, err := g.client.Issues.UpdateIssue(g.projectId, issue.IID, options)
	if err != nil {
		metrics.ReportError("FailedToUpdateGitlabIssue", "gitlab")
		level.Error(g.logger).Log("msg", "failed to update gitlab issue, will try to create new", "err", err, "response", response)
		return err
	}
	level.Info(g.logger).Log("msg", "updated issue in gitlab", "gitlab_issue_id", issue.IID)
	return nil
}

// CreateIssue from the Webhook in Gitlab
func (g *Gitlab) CreateIssue(msg *alertmanager.Webhook) error {
	// Extract grouping labels from the message
	groupingLabels := g.extractGroupingLabels(msg)

	// Check for existing issues with same grouping labels
	matchingIssues, err := g.getOpenIssuesSince(groupingLabels, g.getTimeBefore(g.groupInterval))
	if err != nil {
		level.Warn(g.logger).Log("msg", "listing of open issues to check for duplicates failed , opening a new one even though possible duplicate")
	}

	// Try to render the issue text template
	issueText, err := g.renderIssueTemplate(msg)
	if err != nil {
		return err
	}

	if len(matchingIssues) > 0 {
		// Issues are ordered by created date, we update the first so the newest one.
		issueToUpdate := matchingIssues[0]
		if err := g.updateGitlabIssue(issueToUpdate, issueText); err != nil {
			level.Warn(g.logger).Log("msg", "updating an existing issue failed, opening a new one", "updated_issue_id", issueToUpdate.IID)
		} else {
			return nil
		}
	}
	// Try to create a new issue rather than discarding it after failed update.
	if err := g.createGitlabIssue(msg, groupingLabels, issueText); err != nil {
		return err
	}
	return nil
}

func (g *Gitlab) ping() error {
	level.Debug(g.logger).Log("msg", "trying to ping gitlab", "url", g.client.BaseURL())
	_, err := http.Head(g.client.BaseURL().String())
	if err != nil {
		metrics.ReportError("FailedToPingGitlab", "gitlab")
		level.Error(g.logger).Log("msg", "failed to ping gitlab with HEAD request", "err", err)
		return err
	}
	return nil
}
