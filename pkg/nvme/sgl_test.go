package nvme

import (
	"io"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSglWriter(t *testing.T) {
	sgl := NewScatterList(100, 10)
	assert.Equal(t, 100, sgl.capacity)
	assert.Equal(t, 0, sgl.len)
	assert.Equal(t, 10, len(sgl.buffers))

	writer := NewScatterListWriter(sgl)
	buffer := make([]uint8, 90)
	rand.Read(buffer)
	count, err := writer.Write(buffer)
	assert.Equal(t, count, 90)
	assert.Nil(t, err)
	assert.Equal(t, 100, sgl.capacity)
	assert.Equal(t, 90, sgl.len)

	for i := 0; i < 9; i++ {
		assert.ElementsMatch(t, buffer[10*i:10*(i+1)], sgl.buffers[i])
	}

	// now write 10 more bytes
	buffer2 := make([]uint8, 11)
	rand.Read(buffer2)
	count, err = writer.Write(buffer2)
	assert.Equal(t, 10, count)
	assert.Equal(t, io.ErrShortBuffer, err)
	assert.Equal(t, 100, sgl.capacity)
	assert.Equal(t, 100, sgl.len)

	// Check original buffers that they are not overwritten
	for i := 0; i < 9; i++ {
		assert.ElementsMatch(t, buffer[10*i:10*(i+1)], sgl.buffers[i])
	}

	// check last buffer that it actually contains the data
	assert.ElementsMatch(t, buffer2[:10], sgl.buffers[len(sgl.buffers)-1])
}

func TestSglWriterOverriteBuffer(t *testing.T) {
	sgl := NewScatterList(100, 100)
	assert.Equal(t, 100, sgl.capacity)
	assert.Equal(t, 0, sgl.len)
	assert.Equal(t, 1, len(sgl.buffers))

	writer := NewScatterListWriter(sgl)
	buffer := make([]uint8, 101)
	rand.Read(buffer)
	count, err := writer.Write(buffer)

	assert.Equal(t, count, 100)
	assert.Equal(t, io.ErrShortBuffer, err)
	assert.Equal(t, 100, sgl.capacity)
	assert.Equal(t, 100, sgl.len)

	// check last buffer that it actually contains the data
	assert.ElementsMatch(t, buffer[:100], sgl.buffers[len(sgl.buffers)-1])

	failedBuffer := make([]uint8, 1)
	count, err = writer.Write(failedBuffer)
	assert.Equal(t, count, 0)
	assert.Equal(t, io.ErrShortBuffer, err)
	assert.Equal(t, 100, sgl.capacity)
	assert.Equal(t, 100, sgl.len)
}

func TestSglWriterSmallBuffers(t *testing.T) {
	sgl := NewScatterList(20, 20)
	assert.Equal(t, 20, sgl.capacity)
	assert.Equal(t, 0, sgl.len)

	var buffer1 [10]uint8
	var buffer2 [9]uint8
	rand.Read(buffer1[:])
	rand.Read(buffer2[:])

	writer := NewScatterListWriter(sgl)
	count, err := writer.Write(buffer1[:])
	assert.Equal(t, count, 10)
	assert.Nil(t, err)
	assert.Equal(t, 10, sgl.len)

	count, err = writer.Write(buffer2[:])
	assert.Equal(t, count, 9)
	assert.Nil(t, err)
	assert.Equal(t, 19, sgl.len)

	assert.ElementsMatch(t, buffer1, sgl.buffers[0][:10])
	assert.ElementsMatch(t, buffer2, sgl.buffers[0][10:19])
}

func TestSglReaderReadSingle(t *testing.T) {
	sgl := NewScatterList(100, 100)
	buffer := make([]uint8, 100)
	rand.Read(buffer)
	writer := NewScatterListWriter(sgl)
	writer.Write(buffer)

	outBuffer := make([]uint8, 100)
	reader := NewScatterListReader(sgl)
	count, err := reader.Read(outBuffer)
	assert.Equal(t, count, 100)
	assert.Nil(t, err)
	assert.Equal(t, 100, sgl.capacity)
	assert.Equal(t, 100, sgl.len)
	assert.ElementsMatch(t, buffer, outBuffer)

	failedBuffer := make([]uint8, 1)
	count, err = reader.Read(failedBuffer)
	assert.Equal(t, count, 0)
	assert.Equal(t, io.EOF, err)
}

func TestSglReaderFromMultipleBuffers(t *testing.T) {
	sgl := NewScatterList(100, 10)
	buffer := make([]uint8, 100)
	rand.Read(buffer)
	writer := NewScatterListWriter(sgl)
	writer.Write(buffer)

	outBuffer := make([]uint8, 100)
	reader := NewScatterListReader(sgl)
	count, err := reader.Read(outBuffer)
	assert.Equal(t, count, 100)
	assert.Nil(t, err)
	assert.Equal(t, 100, sgl.capacity)
	assert.Equal(t, 100, sgl.len)
	assert.ElementsMatch(t, buffer, outBuffer)

	failedBuffer := make([]uint8, 1)
	count, err = reader.Read(failedBuffer)
	assert.Equal(t, count, 0)
	assert.Equal(t, io.EOF, err)
}
