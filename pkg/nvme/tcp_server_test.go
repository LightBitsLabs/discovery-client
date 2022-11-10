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
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type clientConn struct {
	conn net.Conn
}

func newClient(t *testing.T, endpoint string) *clientConn {
	conn, err := net.Dial("tcp", endpoint)
	assert.NoError(t, err)
	c := &clientConn{
		conn: conn,
	}
	return c
}

func (c *clientConn) stop() {
	c.conn.Close()
}

func TestTCPServerShutdown(t *testing.T) {
	endpoint := "localhost:33333"
	var crtlID uint16 = 1
	server, err := NewServer(endpoint, nil, "3", crtlID)
	assert.NoError(t, err)
	server.Run()

	for i := 0; i < 100; i++ {
		newClient(t, endpoint)
	}
	server.Stop()
}

func TestTCPServerRunStopToggle(t *testing.T) {
	endpoint := "localhost:33333"
	var crtlID uint16 = 1
	server, err := NewServer(endpoint, nil, "3", crtlID)
	assert.NoError(t, err)
	for i := 0; i < 10; i++ {
		server.Run()

		for i := 0; i < 4; i++ {
			newClient(t, endpoint)
		}

		time.Sleep(time.Second)
		server.Stop()
	}
}
