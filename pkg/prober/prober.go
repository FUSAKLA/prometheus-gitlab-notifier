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

package prober

import (
	"io"
	"net/http"
	"sync"

	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
)

// NewInRouter returns new Prober which registers its endpoints in the Router to provide readiness and liveness endpoints.
func NewInRouter(logger log.FieldLogger, router *mux.Router) *Prober {
	p := &Prober{
		logger:      logger,
		serverReady: nil,
	}
	p.registerInRouter(router)
	return p
}

// Prober holds application readiness/liveness status and provides handlers for reporting it.
type Prober struct {
	logger         log.FieldLogger
	serverReadyMtx sync.RWMutex
	serverReady    error
}

func (p *Prober) registerInRouter(router *mux.Router) {
	router.HandleFunc("/liveness", p.livenessHandler)
	router.HandleFunc("/readiness", p.readinessHandler)
}

func (p *Prober) livenessHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `OK`)
}

func (p *Prober) writeFailedReadiness(w http.ResponseWriter, err error) {
	p.logger.WithField("err", err).Error("readiness probe failed")
	http.Error(w, err.Error(), http.StatusServiceUnavailable)
}

func (p *Prober) readinessHandler(w http.ResponseWriter, _ *http.Request) {
	if err := p.isReady(); err != nil {
		p.writeFailedReadiness(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `OK`)
}

// SetServerNotReady sets the readiness probe to invalid state.
func (p *Prober) SetServerNotReady(err error) {
	p.serverReadyMtx.Lock()
	defer p.serverReadyMtx.Unlock()
	p.logger.WithField("reason", err).Warn("Marking server as not ready")
	p.serverReady = err
}

func (p *Prober) isReady() error {
	p.serverReadyMtx.RLock()
	defer p.serverReadyMtx.RUnlock()
	return p.serverReady
}
