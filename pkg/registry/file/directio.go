package file

import (
	"errors"
	"io"

	"github.com/ncw/directio"
)

// DirectIOReader is a reader that reads data from the underlying reader using direct I/O.
type DirectIOReader struct {
	buf     []byte
	bufSize int
	off     int
	rd      io.Reader
}

var _ io.ByteReader = (*DirectIOReader)(nil)

var _ io.Reader = (*DirectIOReader)(nil)

func NewDirectIOReader(rd io.Reader) *DirectIOReader {
	return &DirectIOReader{
		buf: directio.AlignedBlock(directio.BlockSize),
		rd:  rd,
	}
}

func (d *DirectIOReader) Read(p []byte) (int, error) {
	// read data from the underlying reader if the buffer is empty
	if d.off == d.bufSize {
		var err error
		d.bufSize, err = io.ReadFull(d.rd, d.buf)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return 0, err
		}
		d.off = 0
	}
	// copy data to the buffer
	n := copy(p, d.buf[d.off:])
	d.off += n
	return n, nil
}

func (d *DirectIOReader) ReadByte() (byte, error) {
	panic("ReadByte not implemented, gob.Decode should not be using this")
}
