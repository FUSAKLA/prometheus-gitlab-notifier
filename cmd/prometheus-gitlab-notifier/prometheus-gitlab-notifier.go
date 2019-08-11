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
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/alecthomas/kingpin"
	"github.com/fusakla/prometheus-gitlab-notifier/pkg/alertmanager"
	"github.com/fusakla/prometheus-gitlab-notifier/pkg/api"
	"github.com/fusakla/prometheus-gitlab-notifier/pkg/gitlab"
	"github.com/fusakla/prometheus-gitlab-notifier/pkg/handler"
	"github.com/fusakla/prometheus-gitlab-notifier/pkg/metrics"
	"github.com/fusakla/prometheus-gitlab-notifier/pkg/prober"
	"github.com/fusakla/prometheus-gitlab-notifier/pkg/processor"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/gorilla/mux"
	"github.com/pkg/errors"
)

func setupLogger(debug bool) log.Logger {
	l := log.NewLogfmtLogger(log.NewSyncWriter(os.Stdout))
	l = log.With(l, "ts", log.DefaultTimestamp, "caller", log.DefaultCaller)
	if debug {
		l = level.NewFilter(l, level.AllowDebug())
	} else {
		l = level.NewFilter(l, level.AllowInfo())
	}
	return l
}

func waitForEmptyChannel(logger log.Logger, ch <-chan *alertmanager.Webhook) {
	level.Info(logger).Log("msg", "waiting for all the alerts to be processed")
	for {
		if len(ch) > 0 {
			level.Info(logger).Log("msg", "there are still alerts in the queue, waiting forthem to be processed", "queue_size", len(ch))
			time.Sleep(10 * time.Millisecond)
			continue
		}
		break
	}
	level.Info(logger).Log("msg", "processing of the rest of alerts is done")
}

func startServer(logger log.Logger, r http.Handler) (*http.Server, <-chan error) {
	errCh := make(chan error, 1)
	srv := &http.Server{
		Handler:      handler.Instrumented(logger, r),
		Addr:         *serverAddr,
		WriteTimeout: 5 * time.Second,
		ReadTimeout:  5 * time.Second,
	}
	go func() {
		defer close(errCh)
		level.Info(logger).Log("msg", "Starting prometheus-gitlab-notifier", "addr", "0.0.0.0:9288")
		if err := srv.ListenAndServe(); err != nil {
			if err != http.ErrServerClosed {
				level.Error(logger).Log("msg", "server failed", "error", err)
				errCh <- err
			}
		}
	}()
	return srv, errCh
}

var (
	app                  = kingpin.New("prometheus-gitlab-notifier", "Web server listening for webhooks of alertmanager and creating an issue in Gitlab based on it.")
	debug                = app.Flag("debug", "Enables debug logging.").Bool()
	serverAddr           = app.Flag("server.addr", "Allows to change the address and port at which the server will listen for incoming connections.").Default("0.0.0.0:9288").String()
	gitlabURL            = app.Flag("gitlab.url", "URL of the Gitlab API.").Required().String()
	gitlabTokenFile      = app.Flag("gitlab.token.file", "Path to file containing gitlab token.").Required().ExistingFile()
	projectId            = app.Flag("project.id", "Id of project where to create the issues.").Required().Int()
	groupInterval        = app.Flag("group.interval", "Duration how long back to check for opened issues with the same group labels to append the new alerts to (go duration syntax allowing 'ns', 'us' , 'ms', 's', 'm', 'h').").Default("1h").Duration()
	issueLabels          = app.Flag("issue.label", "Labels to add to the created issue. (Can be passed multiple times)").Strings()
	dynamicIssueLabels   = app.Flag("dynamic.issue.label.name", "Alert label, which is to be propagated to the resulting Gitlab issue as scoped label if present in the received alert. (Can be passed multiple times)").Strings()
	issueTemplatePath    = app.Flag("issue.template", "Path to the issue golang template file.").Default("conf/default_issue.tmpl").ExistingFile()
	queueSizeLimit       = app.Flag("queue.size.limit", "Limit of the alert queue size.").Default("100").Int()
	retryBackoff         = app.Flag("retry.backoff", "Duration how long to wait till next retry (go duration syntax allowing 'ns', 'us' , 'ms', 's', 'm', 'h').").Default("5m").Duration()
	retryLimit           = app.Flag("retry.limit", "Maximum number of retries for single alert. If exceeded it's thrown away.").Default("5").Int()
	gracefulShutdownWait = app.Flag("graceful.shutdown.wait.duration", "Duration how long to wait on graceful shutdown marked as not ready (go duration syntax allowing 'ns', 'us' , 'ms', 's', 'm', 'h').").Default("30s").Duration()
)

func main() {

	kingpin.MustParse(app.Parse(os.Args[1:]))

	// Initiate logging.
	logger := setupLogger(*debug)

	// Initiate Gitlab client.
	gitlabIssueTextTemplate, err := template.ParseFiles(*issueTemplatePath)
	if err != nil {
		level.Error(logger).Log("msg", "invalid gitlab issue template", "file", *issueTemplatePath, "err", err)
		os.Exit(1)
	}
	token, err := ioutil.ReadFile(*gitlabTokenFile)
	if err != nil {
		level.Error(logger).Log("msg", "failed to read token file", "file", gitlabTokenFile, "err", err)
		os.Exit(1)
	}
	g, err := gitlab.New(
		log.With(logger, "component", "gitlab"),
		*gitlabURL,
		strings.TrimSpace(string(token)),
		*projectId,
		gitlabIssueTextTemplate,
		issueLabels,
		dynamicIssueLabels,
		groupInterval,
	)
	if err != nil {
		level.Error(logger).Log("msg", "invalid gitlab configuration")
		os.Exit(1)
	}

	// Start processing all incoming alerts.
	alertChan := make(chan *alertmanager.Webhook, *queueSizeLimit)
	proc := processor.New(log.With(logger, "component", "processor"))
	processCtx, processCancelFunc := context.WithCancel(context.Background())
	defer processCancelFunc()
	proc.Process(processCtx, g, alertChan, *retryLimit, *retryBackoff)

	// Setup routing for HTTP server.
	r := mux.NewRouter()
	// Initialize the main API.
	webhookApi := api.NewInRouter(
		log.With(logger, "component", "api"),
		r.PathPrefix("/api").Subrouter(),
		alertChan,
	)
	// Initialize prober providing readiness and liveness checks.
	readinessProber := prober.NewInRouter(
		log.With(logger, "component", "prober"),
		r.PathPrefix("/").Subrouter(),
	)
	// Initialize metrics handler to serve Prometheus metrics.
	metrics.HandleInRouter(r)

	// Start HTTP server
	_, serverErrorChan := startServer(log.With(logger, "component", "server"), r)

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
			level.Info(logger).Log("msg", "received system signal for graceful shutdown", "signal", sig)
			// Mark server as not ready so no new connections will come.
			readinessProber.SetServerNotReady(errors.New("server is shutting down"))
			// Wait for specified time after marking server not ready so the environment can react on it.
			level.Info(logger).Log("msg", "waiting for graceful shutdown", "duration", gracefulShutdownWait)
			time.Sleep(*gracefulShutdownWait)
			// Stop receiving new alerts.
			webhookApi.Close()
			// Wait for all enqueued alerts to be processed.
			waitForEmptyChannel(logger, alertChan)
			os.Exit(0)
		}
	}

}
