package download

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"gitee.com/zhReimu/steam-go/application/content/cdn"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz/lzma"
)

const (
	vzstdHeaderBytes = 8
	vzstdFooterBytes = 15
	vzipHeaderBytes  = 12
	vzipFooterBytes  = 10
)

func init() {
	if dec, err := zstd.NewReader(nil); err == nil {
		cdn.RegisterZstdDecompressor(zstdChunks{dec: dec})
	}
	cdn.RegisterVZipDecompressor(vzipChunks{})
}

type zstdChunks struct {
	dec *zstd.Decoder
}

func (z zstdChunks) Decompress(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(data) < vzstdHeaderBytes+vzstdFooterBytes || !bytes.HasPrefix(data, []byte("VSZa")) {
		return nil, fmt.Errorf("vzstd: invalid chunk (%d bytes)", len(data))
	}
	return z.dec.DecodeAll(data[vzstdHeaderBytes:len(data)-vzstdFooterBytes], nil)
}

type vzipChunks struct{}

func (vzipChunks) Decompress(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(data) < vzipHeaderBytes+vzipFooterBytes || !bytes.HasPrefix(data, []byte("VZa")) {
		return nil, fmt.Errorf("vzip: invalid chunk (%d bytes)", len(data))
	}
	if data[len(data)-2] != 'z' || data[len(data)-1] != 'v' {
		return nil, fmt.Errorf("vzip: invalid footer")
	}
	props := data[7]
	dictSize := binary.LittleEndian.Uint32(data[8:12])
	compressed := data[vzipHeaderBytes : len(data)-vzipFooterBytes]
	sizeDecompressed := binary.LittleEndian.Uint32(data[len(data)-6 : len(data)-2])

	header := make([]byte, 13)
	header[0] = props
	binary.LittleEndian.PutUint32(header[1:5], dictSize)
	binary.LittleEndian.PutUint64(header[5:13], uint64(sizeDecompressed))

	reader, err := lzma.NewReader(io.MultiReader(bytes.NewReader(header), bytes.NewReader(compressed)))
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if uint32(len(out)) != sizeDecompressed {
		return nil, fmt.Errorf("vzip: size mismatch (%d != %d)", len(out), sizeDecompressed)
	}
	return out, nil
}
