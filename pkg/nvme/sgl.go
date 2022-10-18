package nvme

import (
	"fmt"
	"io"
)

type ScatterList struct {
	buffers  [][]byte
	len      int
	capacity int
}

func NewScatterList(datalen, bufferLen int) *ScatterList {
	buffers := make([][]byte, 0)

	for len := datalen; len > 0; {
		bufferSize := minInt(len, bufferLen)
		buffer := make([]byte, bufferSize)
		buffers = append(buffers, buffer)
		len -= bufferSize
	}

	return &ScatterList{buffers: buffers, len: 0, capacity: datalen}
}

// Len is the amount of data we can write before filling the sgl
func (sgl *ScatterList) Len() int {
	return sgl.capacity - sgl.len
}

func (sgl *ScatterList) Size() int {
	return sgl.capacity
}

func (sgl *ScatterList) String() string {
	return fmt.Sprintf("%+v", sgl.buffers)
}

type scatterListWriter struct {
	sgl    *ScatterList
	offset int
	index  int
}

func (sgl *scatterListWriter) Len() int {
	sum := 0
	for i := 0; i < sgl.index; i++ {
		sum += len(sgl.sgl.buffers[i])
	}
	return sgl.sgl.Size() - (sum + sgl.offset)
}

func (sgl *scatterListWriter) Size() int {
	return sgl.sgl.Size()
}

// Offset offset in bytes from the begining of buffers[index]
func (sgl *scatterListWriter) Offset() int {
	return sgl.offset
}

// Index the buffer number currently free to write
func (sgl *scatterListWriter) Index() int {
	return sgl.index
}

type scatterListReader struct {
	sgl    *ScatterList
	offset int
	index  int
}

func (sgl *scatterListReader) Len() int {
	sum := 0
	for i := 0; i < sgl.index; i++ {
		sum += len(sgl.sgl.buffers[i])
	}
	return sgl.sgl.Size() - (sum + sgl.offset)
}

func (sgl *scatterListReader) Size() int {
	return sgl.sgl.Size()
}

// Offset offset in bytes from the begining of buffers[index]
func (sgl *scatterListReader) Offset() int {
	return sgl.offset
}

// Index the buffer number currently free to write
func (sgl *scatterListReader) Index() int {
	return sgl.index
}

// Read sgl into buffer p
// Return amount of bytes read,  error if SGL is too short
func (reader *scatterListReader) Read(p []byte) (n int, err error) {
	sgl := reader.sgl
	readLen := len(p)
	bytesCopied := 0
	for readLen > 0 && reader.index < len(sgl.buffers) {
		buffer := sgl.buffers[reader.index]
		leftInBuffer := len(buffer) - reader.offset
		bytesToCopy := minInt(leftInBuffer, readLen)
		src := buffer[reader.offset : reader.offset+bytesToCopy]
		dst := p[bytesCopied : bytesCopied+bytesToCopy]
		copy(dst, src)
		reader.offset += bytesToCopy
		bytesCopied += bytesToCopy
		readLen -= bytesToCopy

		// we did not utilize the whole buffer with this read
		if reader.offset < len(buffer) {
			continue
		}

		reader.offset = 0
		reader.index++
	}

	// our SGL is too short to serve the read
	if readLen > 0 {
		return bytesCopied, io.EOF
	}

	return bytesCopied, nil
}

// Writes continious buffer p into sgl
func (writer *scatterListWriter) Write(p []byte) (n int, err error) {
	writeLen := len(p)
	pOffset := 0
	sgl := writer.sgl
	for writeLen > 0 && writer.index < len(sgl.buffers) {
		buffer := sgl.buffers[writer.index]
		bufferLen := len(buffer) - writer.offset
		leftToWrite := minInt(bufferLen, writeLen)
		src := p[pOffset : pOffset+leftToWrite]
		dst := buffer[writer.offset:]
		copy(dst, src)

		writeLen -= leftToWrite
		pOffset += leftToWrite
		sgl.len += leftToWrite
		writer.offset += leftToWrite

		if writer.offset < len(buffer) {
			continue
		}

		writer.offset = 0
		writer.index++
	}

	if writeLen > 0 {
		return pOffset, io.ErrShortBuffer
	}
	return pOffset, nil
}

func NewScatterListWriter(sgl *ScatterList) *scatterListWriter {
	return &scatterListWriter{sgl: sgl, offset: 0, index: 0}
}

func NewScatterListReader(sgl *ScatterList) *scatterListReader {
	return &scatterListReader{sgl: sgl, offset: 0, index: 0}
}
