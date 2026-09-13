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
	"net"
	"runtime"
	"testing"
	"time"
)

// The consumer in tcp_client.go races recvPdu's error channel against
// ctx.Done() and simply walks away when the context wins, so recvPdu must
// never block trying to report an error nobody is listening for. Before the
// fix each such teardown stranded one goroutine forever.
func TestRecvPduDoesNotLeakWhenErrorIsNeverRead(t *testing.T) {
	const cycles = 50

	baseline := runtime.NumGoroutine()
	cancels := make([]context.CancelFunc, 0, cycles)
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()

	for i := 0; i < cycles; i++ {
		local, remote := net.Pipe()
		queue := newNvmeTCPQueue(1, local)
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)

		// Deliberately drop the returned channel: this stands in for the
		// consumer that already returned on ctx.Done().
		_ = queue.recvPdu(ctx)

		// Break the connection so handleRecv fails and recvPdu reports the
		// error. The context is intentionally left live, so the only way out
		// for the receive goroutine is a send that does not block.
		remote.Close()
		local.Close()
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		leaked := runtime.NumGoroutine() - baseline
		if leaked < cycles/2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("recvPdu leaked goroutines: %d still parked after %d failed receive loops", leaked, cycles)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
