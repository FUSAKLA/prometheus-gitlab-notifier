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

package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/fusakla/prometheus-gitlab-notifier/pkg/alertmanager"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/gorilla/mux"
	"github.com/prometheus/alertmanager/notify/webhook"
)

// NewInRouter creates new Api instance which will register it's handlers in the given router.
func NewInRouter(logger log.Logger, r *mux.Router, ch chan<- *alertmanager.Webhook) *Api {
	api := &Api{
		logger:        logger,
		alertChan:     ch,
		receiveAlerts: true,
	}
	api.registerHandlers(r)
	return api
}

// Api defines handler functions for receiving Alertmanager endpoints.
type Api struct {
	logger           log.Logger
	alertChan        chan<- *alertmanager.Webhook
	receiveAlerts    bool
	receiveAlertsMtx sync.RWMutex
}

func (a *Api) registerHandlers(router *mux.Router) {
	router.HandleFunc("/alertmanager", a.webhookHandler)
}

func (a *Api) webhookHandler(w http.ResponseWriter, r *http.Request) {
	if !a.canReceiveAlerts() {
		http.Error(w, "Server is not receiving new alerts.", http.StatusServiceUnavailable)
		return
	}
	var message webhook.Message
	err := json.NewDecoder(r.Body).Decode(&message)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid incomming webhook format. Failed with error: %s", err), http.StatusBadRequest)
		return
	}

	// Push the message to channel
	a.alertChan <- alertmanager.NewWebhookFromAlertmanagerMessage(message)
	level.Debug(a.logger).Log("msg", "enqueued alert for processing", "group_key", message.GroupKey)

	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `Ok, Alert enqueued.`)
}

// Close disabled receiving of new alerts in the API used mainly for graceful shutdown.
func (a *Api) Close() {
	a.receiveAlertsMtx.Lock()
	defer a.receiveAlertsMtx.Unlock()
	a.receiveAlerts = false
	close(a.alertChan)
}

func (a *Api) canReceiveAlerts() bool {
	a.receiveAlertsMtx.RLock()
	defer a.receiveAlertsMtx.RUnlock()
	return a.receiveAlerts
}
