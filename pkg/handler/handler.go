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

package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/fusakla/prometheus-gitlab-notifier/pkg/metrics"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "request_duration_seconds",
		Help:    "Time (in seconds) spent serving HTTP requests.",
		Buckets: prometheus.DefBuckets,
	}, []string{"app", "method", "endpoint", "status_code"})
)

func init() {
	metrics.Register(requestDuration)
}

type instrumentedWriter struct {
	http.ResponseWriter
	status int
}

// WriteHeader writes header to the response.
func (w *instrumentedWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *instrumentedWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	n, err := w.ResponseWriter.Write(b)
	return n, err
}

// Instrumented returns instrumented handler which provides access logging and prometheus metrics for incoming requests.
func Instrumented(logger log.Logger, handler http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := instrumentedWriter{ResponseWriter: w}
		handler.ServeHTTP(&sw, r)
		duration := time.Since(start)
		level.Info(logger).Log("msg", "access log", "uri", r.RequestURI, "method", r.Method, "status", sw.status, "remote_addr", r.RemoteAddr, "duration", duration)
		metricsEndpoint := r.URL.Path
		if sw.status == 404 {
			metricsEndpoint = "non-existing-endpoint"
		}
		requestDuration.WithLabelValues(metrics.AppLabel, r.Method, metricsEndpoint, strconv.Itoa(sw.status)).Observe(float64(duration.Seconds()))
	}
}
