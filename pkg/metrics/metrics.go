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

package metrics

import (
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// AppLabel is constant name of the application used
const AppLabel = "prometheus-gitlab-notifier"

var (
	appVersion  = "unknown"
	gitRevision = "unknown"
	gitBranch   = "unknown"
	gitTag      = "unknown"

	registry     *prometheus.Registry
	errorsTotal  *prometheus.CounterVec
	appBuildInfo *prometheus.CounterVec
)

func init() {
	registry = prometheus.NewRegistry()

	// Metric with information about build AppVersion, golang AppVersion etc/
	appBuildInfo = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "app_build_info",
		Help: "Metadata metric with info about build and AppVersion.",
	}, []string{"app", "version", "revision", "branch", "tag"})
	registry.MustRegister(appBuildInfo)
	appBuildInfo.WithLabelValues(AppLabel, appVersion, gitRevision, gitBranch, gitTag).Inc()

	// Generic metric for reporting errors
	errorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "errors_total",
		Help: "Count of occurred errors.",
	}, []string{"app", "type", "remote_app"})
	registry.MustRegister(errorsTotal)

	// When using custom registry we need to explicitly register the Go and process collectors.
	registry.MustRegister(prometheus.NewGoCollector())
	registry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))

}

// HandleInRouter registers prometheus metrics rendering in given router.
func HandleInRouter(r *mux.Router) {
	r.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
}

// Register new Prometheus metric Collector to the registry.
func Register(m prometheus.Collector) {
	registry.MustRegister(m)
}

// ReportError to errors_total metric global for the whole application.
func ReportError(errorType string, remoteApp string) {
	errorsTotal.WithLabelValues(AppLabel, errorType, remoteApp).Inc()
}
