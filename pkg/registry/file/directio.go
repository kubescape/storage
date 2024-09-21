package file

import (
	"io"
	"sync"
	"unsafe"

	"github.com/spf13/afero"
)

// inspired by https://pkg.go.dev/github.com/essentialkaos/ek/v13/directio

const (
	BlockSize = 4096 // Minimal block size
	AlignSize = 4096 // Align size
)

var blockPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, BlockSize+AlignSize)
	},
}

func readData(f afero.File) ([]byte, error) {
	var buf []byte

	block := allocateBlock()
	blockSize := len(block)
	info, _ := f.Stat()
	chunks := (int(info.Size()) / blockSize) + 1

	for i := 0; i < chunks; i++ {
		n, err := f.ReadAt(block, int64(i*blockSize))

		if err != nil && err != io.EOF {
			return nil, err
		}

		buf = append(buf, block[:n]...)
	}

	freeBlock(block)

	return buf, nil
}

func allocateBlock() []byte {
	block := blockPool.Get().([]byte)

	var offset int

	alg := alignment(block, AlignSize)

	if alg != 0 {
		offset = AlignSize - alg
	}

	return block[offset : offset+BlockSize]
}

func freeBlock(block []byte) {
	blockPool.Put(block)
}

func alignment(block []byte, alignment int) int {
	return int(uintptr(unsafe.Pointer(&block[0])) & uintptr(alignment-1))
}
