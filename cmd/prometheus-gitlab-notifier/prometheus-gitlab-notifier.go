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

package main

import (
	"context"
	_ "embed"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/Masterminds/sprig"
	"github.com/alecthomas/kingpin"
	"github.com/fusakla/prometheus-gitlab-notifier/pkg/alertmanager"
	"github.com/fusakla/prometheus-gitlab-notifier/pkg/api"
	"github.com/fusakla/prometheus-gitlab-notifier/pkg/gitlab"
	"github.com/fusakla/prometheus-gitlab-notifier/pkg/handler"
	"github.com/fusakla/prometheus-gitlab-notifier/pkg/metrics"
	"github.com/fusakla/prometheus-gitlab-notifier/pkg/prober"
	"github.com/fusakla/prometheus-gitlab-notifier/pkg/processor"
	"github.com/gorilla/mux"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

func setupLogger(debug bool, logJson bool) log.FieldLogger {
	l := log.New()
	l.SetOutput(os.Stdout)
	if logJson {
		l.SetFormatter(&log.JSONFormatter{})
	}
	if debug {
		l.SetLevel(log.DebugLevel)
	}
	return l
}

func waitForEmptyChannel(logger log.FieldLogger, ch <-chan *alertmanager.Webhook) {
	logger.Info("waiting for all the alerts to be processed")
	for {
		if len(ch) > 0 {
			logger.WithField("queue_size", len(ch)).Info("there are still alerts in the queue, waiting for them to be processed")
			time.Sleep(10 * time.Millisecond)
			continue
		}
		break
	}
	logger.Info("processing of the rest of alerts is done")
}

func startServer(logger log.FieldLogger, r http.Handler) (*http.Server, <-chan error) {
	errCh := make(chan error, 1)
	srv := &http.Server{
		Handler:      handler.Instrumented(logger, r),
		Addr:         *serverAddr,
		WriteTimeout: 5 * time.Second,
		ReadTimeout:  5 * time.Second,
	}
	go func() {
		defer close(errCh)
		logger.WithField("addr", "0.0.0.0:9629").Info("Starting prometheus-gitlab-notifier")
		if err := srv.ListenAndServe(); err != nil {
			if err != http.ErrServerClosed {
				logger.WithField("err", err).Error("server failed")
				errCh <- err
			}
		}
	}()
	return srv, errCh
}

var (
	app                  = kingpin.New("prometheus-gitlab-notifier", "Web server listening for webhooks of alertmanager and creating an issue in Gitlab based on it.")
	debug                = app.Flag("debug", "Enables debug logging.").Bool()
	logJson              = app.Flag("log.json", "Log in JSON format").Bool()
	serverAddr           = app.Flag("server.addr", "Allows to change the address and port at which the server will listen for incoming connections.").Default("0.0.0.0:9629").String()
	gitlabURL            = app.Flag("gitlab.url", "URL of the Gitlab API.").Default("https://gitlab.com").String()
	gitlabTokenFile      = app.Flag("gitlab.token.file", "Path to file containing gitlab token.").Required().ExistingFile()
	projectId            = app.Flag("project.id", "Id of project where to create the issues.").Required().Int()
	groupInterval        = app.Flag("group.interval", "Duration how long back to check for opened issues with the same group labels to append the new alerts to (go duration syntax allowing 'ns', 'us' , 'ms', 's', 'm', 'h').").Default("1h").Duration()
	issueLabels          = app.Flag("issue.label", "Labels to add to the created issue. (Can be passed multiple times)").Strings()
	dynamicIssueLabels   = app.Flag("dynamic.issue.label.name", "Alert label, which is to be propagated to the resulting Gitlab issue as scoped label if present in the received alert. (Can be passed multiple times)").Strings()
	issueTemplatePath    = app.Flag("issue.template", "Path to the issue golang template file.").ExistingFile()
	queueSizeLimit       = app.Flag("queue.size.limit", "Limit of the alert queue size.").Default("100").Int()
	retryBackoff         = app.Flag("retry.backoff", "Duration how long to wait till next retry (go duration syntax allowing 'ns', 'us' , 'ms', 's', 'm', 'h').").Default("5m").Duration()
	retryLimit           = app.Flag("retry.limit", "Maximum number of retries for single alert. If exceeded it's thrown away.").Default("5").Int()
	gracefulShutdownWait = app.Flag("graceful.shutdown.wait.duration", "Duration how long to wait on graceful shutdown marked as not ready (go duration syntax allowing 'ns', 'us' , 'ms', 's', 'm', 'h').").Default("30s").Duration()
)

//go:embed default_issue.tmpl
var defaultIssueTemplate []byte

func main() {
	var err error
	kingpin.MustParse(app.Parse(os.Args[1:]))

	// Initiate logging.
	logger := setupLogger(*debug, *logJson)

	tpl := template.New("base").Funcs(template.FuncMap(sprig.FuncMap()))

	templateContents := defaultIssueTemplate
	if *issueTemplatePath != "" {
		templateContents, err = os.ReadFile(*issueTemplatePath)
		if err != nil {
			logger.WithFields(log.Fields{"err": err, "file": issueTemplatePath}).Error("failed to read template file")
			os.Exit(1)
		}
	}

	// Initiate Gitlab client.
	gitlabIssueTextTemplate, err := tpl.Parse(string(templateContents))
	if err != nil {
		logger.WithFields(log.Fields{"err": err, "file": issueTemplatePath}).Error("invalid gitlab issue template")
		os.Exit(1)
	}
	token, err := ioutil.ReadFile(*gitlabTokenFile)
	if err != nil {
		logger.WithFields(log.Fields{"err": err, "file": gitlabTokenFile}).Error("failed to read token file")
		os.Exit(1)
	}
	g, err := gitlab.New(
		logger.WithField("component", "gitlab"),
		*gitlabURL,
		strings.TrimSpace(string(token)),
		*projectId,
		gitlabIssueTextTemplate,
		issueLabels,
		dynamicIssueLabels,
		groupInterval,
	)
	if err != nil {
		logger.WithField("err", err).Error("invalid gitlab configuration")
		os.Exit(1)
	}

	// Start processing all incoming alerts.
	alertChan := make(chan *alertmanager.Webhook, *queueSizeLimit)
	proc := processor.New(logger.WithField("component", "processor"))
	processCtx, processCancelFunc := context.WithCancel(context.Background())
	defer processCancelFunc()
	proc.Process(processCtx, g, alertChan, *retryLimit, *retryBackoff)

	// Setup routing for HTTP server.
	r := mux.NewRouter()
	// Initialize the main API.
	webhookApi := api.NewInRouter(
		logger.WithField("component", "api"),
		r.PathPrefix("/api").Subrouter(),
		alertChan,
	)
	// Initialize prober providing readiness and liveness checks.
	readinessProber := prober.NewInRouter(
		logger.WithField("component", "prober"),
		r.PathPrefix("/").Subrouter(),
	)
	// Initialize metrics handler to serve Prometheus metrics.
	metrics.HandleInRouter(r)

	// Start HTTP server
	_, serverErrorChan := startServer(logger.WithField("component", "server"), r)

	// Subscribe to system signals so we can react on them with graceful termination.
	gracefulStop := make(chan os.Signal, 2)
	signal.Notify(gracefulStop, syscall.SIGTERM)
	signal.Notify(gracefulStop, syscall.SIGINT)

	// It the server fails or we receive signal to gracefully shut down we wait till the alert queue is processed(empty).
	for {
		select {
		case <-serverErrorChan:
			// If server failed just wait for all the alerts to be processed.
			waitForEmptyChannel(logger, alertChan)
			os.Exit(1)
		case sig := <-gracefulStop:
			logger.WithField("signal", sig).Info("received system signal for graceful shutdown")
			// Mark server as not ready so no new connections will come.
			readinessProber.SetServerNotReady(errors.New("server is shutting down"))
			// Wait for specified time after marking server not ready so the environment can react on it.
			logger.WithField("duration", gracefulShutdownWait).Info("waiting for graceful shutdown")
			time.Sleep(*gracefulShutdownWait)
			// Stop receiving new alerts.
			webhookApi.Close()
			// Wait for all enqueued alerts to be processed.
			waitForEmptyChannel(logger, alertChan)
			os.Exit(0)
		}
	}

}
