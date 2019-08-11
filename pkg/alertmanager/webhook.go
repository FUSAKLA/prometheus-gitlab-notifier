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

package alertmanager

import (
	"sync"

	"github.com/prometheus/alertmanager/notify/webhook"
)

// NewWebhookFromAlertmanagerMessage returns new Webhook wrapping the original Alertmanager webhook.message.
func NewWebhookFromAlertmanagerMessage(message webhook.Message) *Webhook {
	return &Webhook{
		Message:    message,
		retryCount: 0,
	}
}

// Webhook is wrapper for the Alertmanager webhook.message adding retry counter.
type Webhook struct {
	webhook.Message
	retryCount int
	retryMtx   sync.RWMutex
}

// Retry increments number of retries for the Webhook.
func (w *Webhook) Retry() {
	w.retryMtx.Lock()
	defer w.retryMtx.Unlock()
	w.retryCount++
}

// RetryCount returns number of retries for the given alertmanager message.
func (w *Webhook) RetryCount() int {
	w.retryMtx.RLock()
	defer w.retryMtx.RUnlock()
	return w.retryCount
}
