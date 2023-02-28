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

package nvmehost

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"syscall"

	"github.com/google/uuid"
	"github.com/lightbitslabs/discovery-client/pkg/nvme"
	"github.com/sirupsen/logrus"
)

//#cgo CFLAGS: -I../
//#include <linux/nvme-tcp.h>
import "C"

const (
	hostIDPath = "/etc/nvme/hostid"
	dialerTmo  = time.Second * 1
)

// NvmeDiscPageEntry struct represent discovery log page that will be returned from discover method
type NvmeDiscPageEntry struct {
	PortID  uint16
	CntlID  uint16
	TrsvcID uint16
	Subnqn  string
	Traddr  string
	SubType nvme.SubsystemType
}

type DiscoverRequest struct {
	Transport string
	Traddr    string
	Trsvcid   int
	Hostnqn   string
	Hostaddr  string
	Kato      time.Duration
	HostIface string
}

// TCPClient tcp based client API
type TCPClient interface {
	Stop() error
	Discover(discoverRequest *DiscoverRequest) ([]*NvmeDiscPageEntry, error)
	AENChan() <-chan interface{}
	KAChan() chan interface{}
}

// tcpClient tcp based client
type tcpClient struct {
	remoteAddress            string
	wg                       sync.WaitGroup
	tcpConn                  *net.TCPConn
	keepAlivePeriod          time.Duration
	log                      *logrus.Entry
	tcpQ                     *tcpQueue
	aenCh                    chan interface{}
	ctx                      context.Context
	cancel                   context.CancelFunc
	logPagePaginationEnabled bool
	nvmeHostIDPath           string
}

// NewClient creates NVMeTCP client
func NewClient(logPagePaginationEnabled bool, nvmeHostIDPath string) TCPClient {
	ctx, cancel := context.WithCancel(context.Background())
	client := &tcpClient{
		keepAlivePeriod:          dialerTmo, // timeout of dialer
		log:                      logrus.WithFields(logrus.Fields{}),
		aenCh:                    make(chan interface{}),
		ctx:                      ctx,
		cancel:                   cancel,
		logPagePaginationEnabled: logPagePaginationEnabled,
		nvmeHostIDPath:           nvmeHostIDPath,
	}
	return client
}

func (client *tcpClient) KAChan() chan interface{} {
	return client.tcpQ.keepAliveDone()
}

func removeDash(str string) string {
	str = strings.ReplaceAll(str, "-", "")
	str = strings.TrimSpace(str)
	return str
}

func isValidUUID(u string) bool {
	_, err := uuid.Parse(u)
	return err == nil
}

func (client *tcpClient) AENChan() <-chan interface{} {
	return client.aenCh
}

func (client *tcpClient) getHostID() string {
	// if host-id file doesn't exist generate it.
	var id string
	dat, err := ioutil.ReadFile(client.nvmeHostIDPath)
	if err != nil || string(dat) == "" {
		id := uuid.New().String() + "\n"
		client.log.Debugf("creating hostID file at %v", client.nvmeHostIDPath)
		// make sure this folder exists before we create the hostid file.
		if err := os.MkdirAll(filepath.Dir(client.nvmeHostIDPath), 0755); err != nil {
			client.log.WithError(err).Errorf("failed to create %s folder", filepath.Dir(client.nvmeHostIDPath))
			panic(err)
		}
		err = ioutil.WriteFile(client.nvmeHostIDPath, []byte(id), 0644)
		if err != nil {
			client.log.WithError(err).Errorf("failed to write to %s file", client.nvmeHostIDPath)
			panic(err)
		}
		return removeDash(string(id))
	}
	id = removeDash(string(dat))
	return id
}

func bindSocketToDevice(iface string) func(network, address string, c syscall.RawConn) error {
	if iface == "" {
		return nil
	}

	return func(network, address string, c syscall.RawConn) error {
		var operr error = nil

		fn := func(s uintptr) {
			operr = syscall.BindToDevice(int(s), iface)
		}

		if err := c.Control(fn); err != nil {
			return err
		}
		return operr
	}
}

// Run starts accepting tcp connections on NVMe server
func (client *tcpClient) Discover(discoverRequest *DiscoverRequest) ([]*NvmeDiscPageEntry, error) {
	client.log.Debugf("enter discover")
	client.remoteAddress = discoverRequest.Traddr
	hostID := client.getHostID()
	if isValidUUID(hostID) == false {
		panic("invalid host id")
	}

	addr := net.JoinHostPort(client.remoteAddress, strconv.Itoa(discoverRequest.Trsvcid))
	dialer := net.Dialer{Control: bindSocketToDevice(discoverRequest.HostIface), Timeout: client.keepAlivePeriod}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	// conversion
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return nil, fmt.Errorf("conversion failed")
	}
	client.tcpConn = tcpConn
	client.tcpQ = newNvmeTCPQueue(1, conn)

	// now the code become async and we need to use the sq completion queue.
	client.wg.Add(1)
	go func() {
		defer client.wg.Done()
		select {
		case <-client.ctx.Done():
			return
		// recvPdu creates a tmp chan
		case _, alive := <-client.tcpQ.recvPdu(client.ctx):
			if !alive {
				client.log.Debugf("rcvPdu closed")
			}
			// instead of alert aen channel
			// let it find out by itself through KeepAlive channel
			// and end this goroutine for discover request
		}
	}()

	if err := client.tcpQ.sendNvmeInitConnection(); err != nil {
		return nil, err
	}

	if err := client.tcpQ.sendConnectRequest(client.ctx, discoverRequest.Hostnqn, hostID); err != nil {
		//client.log.WithError(err).Errorf("NVMe connect failed")
		return nil, err
	}

	err = client.tcpQ.setProperties(client.ctx, false)
	if err != nil {
		//client.log.WithError(err).Errorf("NVMe set feature failed")
		return nil, err
	}
	if err := client.tcpQ.sendIdentifyRequest(client.ctx); err != nil {
		return nil, err
	}

	if err := client.tcpQ.sendAsyncEventSetFeature(client.ctx); err != nil {
		return nil, err
	}

	entries, err := client.tcpQ.getLogPageEntries(client.ctx, client.logPagePaginationEnabled)
	if err != nil {
		return nil, err
	}
	response := []*NvmeDiscPageEntry{}
	for _, entry := range entries {
		targetServiceID, err := strconv.Atoi(strings.TrimRight(string(entry.TrsvcID[:]), "\x00"))
		if err != nil {
			client.log.WithError(err).Errorf("failed to parse entry service id")
			targetServiceID = 0
		}

		res := &NvmeDiscPageEntry{
			PortID:  entry.PortID,
			CntlID:  entry.CntlID,
			SubType: entry.SubType,
			TrsvcID: uint16(targetServiceID),
			Subnqn:  strings.TrimRight(string(entry.Subnqn[:]), "\x00"),
			Traddr:  strings.TrimRight(string(entry.Traddr[:]), "\x00"),
		}
		response = append(response, res)
	}

	if discoverRequest.Kato > 0 {
		client.log.Debugf("started routines")
		client.pollAEN()
		go client.tcpQ.keepAlive(client.ctx, discoverRequest.Kato)
	}

	return response, nil
}

func (client *tcpClient) pollAEN() error {
	client.wg.Add(1)
	go func() {
		defer client.wg.Done()
		for {

			request, err := client.tcpQ.handleAEN(client.ctx)
			if request == nil && err == nil {
				// means we returned due to context done,
				// need to break the loop
				return
			}
			if err != nil {
				continue
			}
			client.log.Debugf("Got AEN! request: %s ...", request.String())
			client.aenCh <- request
		}
	}()

	return nil
}
func (client *tcpClient) ClearChannels() {
	for {
		select {
		case <-client.aenCh:
			continue
		default:
			return
		}
	}
}

func (client *tcpClient) Stop() error {
	if client.tcpQ != nil {
		if err := client.tcpQ.setControllerConfiguration(client.ctx, 0x464001); err != nil {
			client.log.WithError(err).Debug("failed to set ctrl back, (stop recieving commands)")
		}
	}
	// close subroutines
	client.cancel()

	if client.tcpQ != nil {
		//client.log.Info("destroy tcp queue..")
		client.tcpQ.destroy()
		client.tcpQ = nil
	}
	// client.log.Info("closing tcp sockets...")
	if client.tcpConn != nil {
		client.tcpConn.Close()
		client.tcpConn = nil
	}
	client.ClearChannels()
	client.wg.Wait()

	close(client.aenCh)

	return nil
}
