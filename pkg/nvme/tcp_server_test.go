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
