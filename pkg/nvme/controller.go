// Copyright 2016--2022 Lightbits Labs Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// you may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package nvme

//#cgo CFLAGS: -I/usr/include
//#include <linux/nvme.h>
import "C"

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lightbitslabs/discovery-client/pkg/metrics"
	"github.com/sirupsen/logrus"
)

const (
	nvmetDiscoveryKatoMsec             = 2 * time.Minute
	maxPendingAsyncEventsRequestsLimit = 4
)

type NvmetAsyncEvent struct {
	hostNqn   string
	eventType uint8
	eventInfo uint8
	logPage   uint8
}

func (aen *NvmetAsyncEvent) result() uint32 {
	return uint32(aen.eventType) | (uint32(aen.eventInfo) << 8) | (uint32(aen.logPage) << 16)
}

type Controller interface {
	ID() uint16
	HostNqn() string
	NotifyAsyncEvent(hostNqn string)
}

type nvmeController struct {
	hostNqn                   string
	id                        uint16
	controllerID              uint16
	hostid                    string
	cap                       uint64
	cc                        uint32
	csts                      uint32
	lock                      sync.Mutex
	keepAliveExpiredCh        chan bool
	kato                      time.Duration
	katoTimer                 *time.Timer
	aenBitEnabled             bool
	rae                       bool
	asyncEventPendingRequests []*AsyncEventRequest
	asyncEventsChan           chan *NvmetAsyncEvent
	wg                        sync.WaitGroup
	ctx                       context.Context
	cancel                    context.CancelFunc
	log                       *logrus.Entry
}

func newController(id uint16, controllerID uint16, request *AdminConnectRequest, connectData *ConnectData) (*nvmeController, uint16) {
	// convent string to uuid - validated by calling method that it is valid
	hostID, _ := uuid.FromBytes([]byte(connectData.HostID))
	ctrl := &nvmeController{
		hostNqn:                   connectData.HostNqn,
		hostid:                    hostID.String(),
		id:                        id,
		controllerID:              controllerID,
		asyncEventPendingRequests: []*AsyncEventRequest{},
		asyncEventsChan:           make(chan *NvmetAsyncEvent),
		keepAliveExpiredCh:        make(chan bool),
		log:                       logrus.WithFields(logrus.Fields{"ctrl_id": id, "host_nqn": connectData.HostNqn, "host_id": hostID.String()}),
	}
	ctrl.ctx, ctrl.cancel = context.WithCancel(context.Background())

	ctrl.initCap()
	kato := time.Duration(request.Cmd.Kato) * time.Millisecond
	if kato.Seconds() == 0 {
		kato = nvmetDiscoveryKatoMsec
	}
	ctrl.kato = kato

	ctrl.wg.Add(2)
	ctrl.keepAliveHandler(&ctrl.wg)
	ctrl.handleAENEvents(&ctrl.wg)

	ctrl.log.WithFields(logrus.Fields{"qid": request.queue.sq.qID}).Debugf("Created controller")
	return ctrl, C.NVME_SC_SUCCESS
}

func (ctrl *nvmeController) HostNqn() string {
	return ctrl.hostNqn
}

func (ctrl *nvmeController) ID() uint16 {
	return ctrl.id
}

func (ctrl *nvmeController) ControllerID() uint16 {
	return ctrl.controllerID
}

func (ctrl *nvmeController) keepAliveHandler(wg *sync.WaitGroup) {
	ctrl.log.Debugf("initiate keep alive handler with timeout: %s", ctrl.kato)
	ctrl.katoTimer = time.NewTimer(ctrl.kato)
	go func() {
		defer wg.Done()
		select {
		case <-ctrl.katoTimer.C:
			if (ctrl.csts & C.NVME_CSTS_CFS) == 0 {
				ctrl.csts |= C.NVME_CSTS_CFS
				ctrl.log.Infof("signaling KA expired")
				ctrl.keepAliveExpiredCh <- true
			}
		case <-ctrl.ctx.Done():
			ctrl.log.Info("aborting keep alive handler")
		}
		ctrl.katoTimer.Stop()
	}()
}

func (ctrl *nvmeController) initCap() {
	/* command sets supported: NVMe command set: */
	ctrl.cap = uint64(1) << 37
	/* CC.EN timeout in 500msec units: */
	ctrl.cap |= uint64(15) << 24
	/* maximum queue entries supported: */
	ctrl.cap |= nvmetQueueSize - 1
}

func (ctrl *nvmeController) clear() {
	/* XXX: tear down queues? */
	ctrl.csts &= ^uint32(C.NVME_CSTS_RDY)
	ctrl.cc = 0
}

func nvmetCCEn(cc uint32) bool {
	return ((cc >> C.NVME_CC_EN_SHIFT) & 0x1) != 0
}

func nvmetCCShn(cc uint32) bool {
	return ((cc >> C.NVME_CC_SHN_SHIFT) & 0x3) != 0
}

func nvmetCCIOSqes(cc uint32) uint8 {
	return uint8((cc >> C.NVME_CC_IOSQES_SHIFT)) & 0xf
}

func nvmetCCIOCqes(cc uint32) uint8 {
	return uint8((cc >> C.NVME_CC_IOCQES_SHIFT)) & 0xf
}

func nvmetCCMps(cc uint32) uint8 {
	return uint8(cc>>C.NVME_CC_MPS_SHIFT) & 0xf
}

func nvmetCCcss(cc uint32) uint8 {
	return uint8(cc>>C.NVME_CC_CSS_SHIFT) & 0x7
}

func nvmetCCAms(cc uint32) uint8 {
	return uint8(cc>>C.NVME_CC_AMS_SHIFT) & 0x7
}

func (ctrl *nvmeController) start() {

	if nvmetCCIOSqes(ctrl.cc) != C.NVME_NVM_IOSQES ||
		nvmetCCIOCqes(ctrl.cc) != C.NVME_NVM_IOCQES ||
		nvmetCCMps(ctrl.cc) != 0 ||
		nvmetCCAms(ctrl.cc) != 0 ||
		nvmetCCcss(ctrl.cc) != 0 {
		ctrl.csts = C.NVME_CSTS_CFS
		return
	}

	ctrl.csts = C.NVME_CSTS_RDY

	// TODO: sashas restart kato here
}

func (ctrl *nvmeController) updateControllerConfiguration(value uint32) {
	ctrl.lock.Lock()
	defer ctrl.lock.Unlock()
	old := ctrl.cc
	ctrl.cc = value

	if nvmetCCEn(ctrl.cc) && !nvmetCCEn(old) {
		ctrl.start()
	}
	if !nvmetCCEn(ctrl.cc) && nvmetCCEn(old) {
		ctrl.clear()
	}
	if nvmetCCShn(ctrl.cc) && !nvmetCCShn(old) {
		ctrl.clear()
		ctrl.csts |= C.NVME_CSTS_SHST_CMPLT
	}

	if !nvmetCCShn(ctrl.cc) && nvmetCCShn(old) {
		ctrl.csts &= ^uint32(C.NVME_CSTS_SHST_CMPLT)
	}
}

func (ctrl *nvmeController) keepAliveExpiredChan() <-chan bool {
	return ctrl.keepAliveExpiredCh
}

func (ctrl *nvmeController) delete() {
	ctrl.log.Infof("deleting controller")
	ctrl.lock.Lock()
	defer ctrl.lock.Unlock()
	// this will make us close the handling events look of the controller.
	ctrl.cancel()

	ctrl.log.Infof("waiting for AEN and KATO goroutines to finish")
	ctrl.wg.Wait()
	ctrl.log.Infof("controller deleted")
}

func (ctrl *nvmeController) resetKatoTimer() {
	ctrl.katoTimer.Reset(ctrl.kato)
}

// registerAsyncEventRequest add to chan the request coming from the user via nvmet
// to get AEN from DS.
func (ctrl *nvmeController) registerAsyncEventRequest(request *AsyncEventRequest) {
	ctrl.lock.Lock()
	defer ctrl.lock.Unlock()

	if len(ctrl.asyncEventPendingRequests) >= maxPendingAsyncEventsRequestsLimit {
		ctrl.log.Warnf("reached max pending async requests: %d", maxPendingAsyncEventsRequestsLimit)
		completion := NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_ASYNC_LIMIT|C.NVME_SC_DNR)
		request.queue.completeRequest(request, completion)
		return
	}
	ctrl.log.Infof("adding AEN request from client. request: %d, ctrl.asyncEventPendingRequests: %d", request.CmdID, len(ctrl.asyncEventPendingRequests))
	ctrl.asyncEventPendingRequests = append(ctrl.asyncEventPendingRequests, request)
}

// NotifyAsyncEvent add notification coming from DS about a change to the Targets model.
// it will trigger response to all the registered AEN requests.
func (ctrl *nvmeController) NotifyAsyncEvent(hostNqn string) {
	if !ctrl.aenEnabled() {
		logrus.Infof("AEN disabled hence skip notify async event on controller: %d", ctrl.ID())
		return
	}

	event := &NvmetAsyncEvent{
		hostNqn:   hostNqn,
		eventType: C.NVME_AER_TYPE_NOTICE,
		eventInfo: C.NVME_AER_NOTICE_DISC_CHANGED,
		logPage:   C.NVME_LOG_DISC,
	}
	ctrl.asyncEventsChan <- event
}

func (ctrl *nvmeController) setAENValue(value uint32) {
	var mask uint32 = 1 << nvmeAENBitDiscChange
	if (mask & value) != 0 {
		ctrl.aenBitEnabled = true
	} else {
		ctrl.aenBitEnabled = false
	}
	ctrl.log.Infof("setting aenBitEnabled to: %t", ctrl.aenBitEnabled)
}

func (ctrl *nvmeController) setRetainAsynchronousEvent(rae bool) {
	logrus.Warnf("RAE set to %t. notice that we don't use the rae information for now and skip it!!!", rae)
	ctrl.rae = rae
}

func (ctrl *nvmeController) aenEnabled() bool {
	if ctrl.aenBitEnabled {
		return true
	}
	return false
}

func (ctrl *nvmeController) handleAENEvents(wg *sync.WaitGroup) {
	go func() {
		defer wg.Done()
		done := false
		for !done {
			select {
			case event := <-ctrl.asyncEventsChan:
				ctrl.lock.Lock()

				if len(ctrl.asyncEventPendingRequests) > 0 {
					// pop first item from slice
					request := ctrl.asyncEventPendingRequests[0]
					ctrl.asyncEventPendingRequests = append(ctrl.asyncEventPendingRequests[:0], ctrl.asyncEventPendingRequests[1:]...)
					// return the result to the caller
					ctrl.log.Infof("signalling AEN on command_id: %d, event: %+v", request.CommandID(), event)
					completion := NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_SUCCESS)
					completion.Result.setU32Result(event.result())
					request.queue.completeRequest(request, completion)
					metrics.Metrics.AENSentTotal.WithLabelValues(request.queue.serviceID, event.hostNqn).Inc()
				}
				ctrl.lock.Unlock()
			case <-ctrl.ctx.Done():
				done = true
			}
		}
		ctrl.log.Info("AEN handler done")
	}()
}

func (ctrl *nvmeController) setKato(kato time.Duration) {
	ctrl.log.Infof("setting kato to: %s", kato.String())
	ctrl.kato = kato
}
