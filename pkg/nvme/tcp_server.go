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

import (
	"context"
	"math"
	"net"
	"reflect"
	"sync"

	"github.com/lightbitslabs/discovery-client/pkg/pools"
	"github.com/sirupsen/logrus"
)

//#include <linux/nvme-tcp.h>
import "C"

// TCPServer tcp based server API
type TCPServer interface {
	Run()
	Stop()
}

// tcpServer tcp based server
type tcpServer struct {
	isAlive                     bool
	address                     string
	socket                      net.Listener
	serviceID                   string
	idPool                      pools.BoundedIDPool
	discoverySubsys             DiscoverySubsystem
	wg                          sync.WaitGroup
	controllerID                uint16
	log                         *logrus.Entry
	doneWorkerChan              chan uint32
	closedWorkerHandlerDoneChan chan bool
	lock                        sync.Mutex
	ctx                         context.Context
	cancel                      context.CancelFunc
}

// NewServer creates nvmeTCP server
func NewServer(address string, discoverySubsys DiscoverySubsystem, serviceID string, controllerID uint16) (TCPServer, error) {
	server := &tcpServer{
		serviceID:       serviceID,
		address:         address,
		idPool:          pools.NewBoundedIDPool(C.NVME_CNTLID_MAX),
		discoverySubsys: discoverySubsys,
		controllerID:    controllerID,
		doneWorkerChan:  make(chan uint32),
	}
	return server, nil
}

// Run starts accepting tcp connections on NVMe server
func (server *tcpServer) Run() {
	if server.isAlive {
		logrus.Infof("server already running")
		return
	}

	server.ctx, server.cancel = context.WithCancel(context.Background())
	tcpSocket, err := net.Listen("tcp", server.address)
	if err != nil {
		logrus.WithError(err).Errorf("failed to listen on tcp: %s", server.address)
		return
	}
	server.socket = tcpSocket
	server.isAlive = true

	server.closedWorkerHandlerDoneChan = server.closedWorkerHandler()

	go func() {
		for server.isAlive {
			logrus.Infof("tcp server started accepting connections on: %s", server.address)
			connection, err := server.socket.Accept()
			if err != nil {
				if server.isAlive {
					logrus.WithError(err).Errorf("failed to accept connection")
				} else {
					logrus.Infof("tcp server stopped accepting connections on: %s", server.address)
				}
				break
			}

			w, err := newWorker(server.ctx, server, connection, &server.wg)
			if err != nil {
				logrus.WithError(err).Errorf("failed to create worker for connection: %q", connection.RemoteAddr())
				continue
			}
			server.wg.Add(1)
			w.run()
		}
		logrus.Infof("tcp server-socket stopped")
	}()
}

// Stop nvme server. drain all workers and nvme connections.
func (server *tcpServer) Stop() {
	if !server.isAlive {
		return
	}
	logrus.Infof("setting server isAlive state to %t...", server.isAlive)
	server.isAlive = false
	logrus.Infof("closing listening socket...")
	server.socket.Close()

	logrus.Infof("starting to drain %d workers", server.idPool.Used())
	server.cancel()
	server.wg.Wait()

	server.doneWorkerChan <- math.MaxUint32
	<-server.closedWorkerHandlerDoneChan
	logrus.Infof("drained all workers. currently have %d workers. tcpServer stop ended", server.idPool.Used())
}

// handleClosedWorker will cleanup the worker resource on the server side
// once the worker has signalled it was deleted.
// we would remove it from the `workers` list and would return the id to the pool.
func (server *tcpServer) closedWorkerHandler() chan bool {
	ch := make(chan bool)
	go func() {
		isDone := false
		for !isDone {
			workerID := <-server.doneWorkerChan
			if workerID == math.MaxUint32 {
				logrus.Infof("closedWorkerHandler signalled to stop")
				isDone = true
				continue
			}
			server.idPool.Put(uint32(workerID))
			logrus.Infof("delete worker %d done. workers left: %d", workerID, server.idPool.Used())
		}
		close(ch)
	}()

	return ch
}

func needsDataOut(request Request) bool {
	val := !request.isWrite() && request.dataLen() > 0 &&
		request.Completion().Status == C.NVME_SC_SUCCESS
	logrus.Debugf("needsDataOut: %t, is writable: %t, dataLen: %d, response status: %#02x. request type: %s, command ID: %#04x",
		val, request.isWrite(),
		request.dataLen(),
		request.Completion().Status,
		reflect.TypeOf(request).String(), request.CommandID())
	return val
}

type worker struct {
	id         uint32
	server     *tcpServer
	connection net.Conn
	queue      *tcpQueue
	wg         *sync.WaitGroup
	log        *logrus.Entry
	ctx        context.Context
	cancel     context.CancelFunc
}

func newWorker(ctx context.Context, server *tcpServer, connection net.Conn, wg *sync.WaitGroup) (*worker, error) {
	queueID, err := server.idPool.Get()
	if err != nil {
		logrus.WithError(err).Errorf("can't accept more connections")
		return nil, err
	}
	w := &worker{
		server:     server,
		connection: connection,
		wg:         wg,
		id:         queueID,
		log:        logrus.WithFields(logrus.Fields{"worker_id": queueID, "remote_address": connection.RemoteAddr()}),
	}
	w.ctx, w.cancel = context.WithCancel(ctx)

	w.queue = newNvmeTCPQueue(server.discoverySubsys, uint16(queueID), connection, server.serviceID, server.controllerID)
	w.log.Infof("created worker with queue id %v", w.queue.id)
	w.queue.ioWork()
	return w, nil
}

// all that run does is just waiting for the queue to close via a KA
// or a closed TCP connection, or that the server will abort the connection
// and we will cleanup the queue
// we would signal to the server that we are done
func (w *worker) run() {
	go func() {
		defer w.wg.Done()

		select {
		case <-w.ctx.Done():
			w.queue.destroy()
		case <-w.queue.doneChan():
			w.queue.destroy()
		}
		w.log.Infof("worker loop stopped. signalling done")
		w.server.doneWorkerChan <- w.id
	}()
}
