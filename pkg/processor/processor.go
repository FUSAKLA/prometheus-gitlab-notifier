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

package processor

import (
	"context"
	"time"

	"github.com/fusakla/prometheus-gitlab-notifier/pkg/alertmanager"
	"github.com/fusakla/prometheus-gitlab-notifier/pkg/gitlab"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

var (
	processedItems = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "prometheus_gitlab_notifier_processed_alerts_processed_total",
		Help: "Count of processed alerts.",
	})
	retryCount = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "prometheus_gitlab_notifier_processed_alerts_retried_total",
		Help: "Count of retries.",
	})
)

func init() {
	prometheus.MustRegister(processedItems)
	prometheus.MustRegister(retryCount)
}

// New returns new processor which handles the alert queue and retrying.
func New(logger log.FieldLogger) *processor {
	return &processor{
		logger: logger,
	}
}

type processor struct {
	logger log.FieldLogger
}

// Process processes alerts from the given channel and creates Gitlab issues from them.
func (p *processor) Process(ctx context.Context, gitlab *gitlab.Gitlab, alertChannel chan *alertmanager.Webhook, retryLimit int, retryBackoff time.Duration) {
	doneChannel := make(chan bool, 1)
	go func() {
		defer close(doneChannel)
		for {
			select {
			case <-ctx.Done():
				return
			case alert, ok := <-alertChannel:
				if !ok {
					return
				}
				p.logger.WithField("group_key", alert.GroupKey).Debug("fetched alert from queue for processing")
				if err := gitlab.CreateIssue(alert); err != nil {
					if alert.RetryCount() >= retryLimit-1 {
						p.logger.WithFields(log.Fields{"group_key": alert.GroupKey, "retry_count": retryLimit}).Warn("alert exceeded maximum number of retries, dropping it")
						continue
					}
					go func() {
						time.Sleep(retryBackoff)
						alert.Retry()
						alertChannel <- alert
						retryCount.Inc()
						p.logger.WithFields(log.Fields{"group_key": alert.GroupKey, "retry_backoff": retryBackoff}).Warn("added alert to queue for retrying ")
					}()
				}
				processedItems.Inc()
			}
		}
	}()
}
