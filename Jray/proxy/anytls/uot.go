package anytls

import (
	"encoding/binary"
	"io"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
)

type uotRequest struct {
	isConnect   bool
	destination net.Destination
}

func readUoTRequest(r io.Reader) (uotRequest, error) {
	var flag [1]byte
	if _, err := io.ReadFull(r, flag[:]); err != nil {
		return uotRequest{}, err
	}
	isConnect := flag[0] != 0
	dest, err := readSocksAddr(r, net.Network_UDP, !isConnect)
	if err != nil {
		return uotRequest{}, err
	}
	return uotRequest{
		isConnect:   isConnect,
		destination: dest,
	}, nil
}

func writeUoTRequest(b *buf.Buffer, isConnect bool, dest net.Destination) error {
	var flag byte
	if isConnect {
		flag = 1
	}
	if err := b.WriteByte(flag); err != nil {
		return err
	}
	return writeSocksAddr(b, dest)
}

type uotReader struct {
	br          *buf.BufferedReader
	src         buf.TimeoutReader
	isConnect   bool
	connectDest net.Destination
	first       buf.MultiBuffer
}

func newUoTReader(br *buf.BufferedReader, isConnect bool, connectDest net.Destination) *uotReader {
	var src buf.TimeoutReader
	if tr, ok := br.Reader.(buf.TimeoutReader); ok {
		src = tr
	}
	return &uotReader{
		br:          br,
		src:         src,
		isConnect:   isConnect,
		connectDest: connectDest,
	}
}

func (r *uotReader) setFirst(mb buf.MultiBuffer) {
	r.first = mb
}

func (r *uotReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	if !r.first.IsEmpty() {
		mb := r.first
		r.first = nil
		return mb, nil
	}

	dest := r.connectDest
	if !r.isConnect {
		var err error
		dest, err = readUoTAddr(r.br)
		if err != nil {
			return nil, err
		}
	}

	var lenBuf [2]byte
	if _, err := io.ReadFull(r.br, lenBuf[:]); err != nil {
		return nil, err
	}
	pktLen := int(binary.BigEndian.Uint16(lenBuf[:]))

	var b *buf.Buffer
	if pktLen <= buf.Size {
		b = buf.New()
	} else {
		b = buf.NewWithSize(int32(pktLen))
	}
	if pktLen > 0 {
		if _, err := b.ReadFullFrom(r.br, int32(pktLen)); err != nil {
			b.Release()
			return nil, err
		}
	}
	d := dest
	b.UDP = &d
	return buf.MultiBuffer{b}, nil
}

func (r *uotReader) ReadMultiBufferTimeout(d time.Duration) (buf.MultiBuffer, error) {
	if !r.first.IsEmpty() {
		mb := r.first
		r.first = nil
		return mb, nil
	}
	if r.br.BufferedBytes() == 0 && r.src != nil {
		mb, err := r.src.ReadMultiBufferTimeout(d)
		if err != nil {
			return nil, err
		}
		if !mb.IsEmpty() {
			r.br.Buffer, _ = buf.MergeMulti(r.br.Buffer, mb)
		}
	}
	return r.ReadMultiBuffer()
}

func (r *uotReader) Interrupt() {
	if !r.first.IsEmpty() {
		r.first = buf.ReleaseMulti(r.first)
	}
	common.Interrupt(r.br.Reader)
}

type uotWriter struct {
	writer      buf.Writer
	isConnect   bool
	defaultDest net.Destination
}

func newUoTWriter(writer buf.Writer, isConnect bool, defaultDest net.Destination) *uotWriter {
	return &uotWriter{
		writer:      writer,
		isConnect:   isConnect,
		defaultDest: defaultDest,
	}
}

func (w *uotWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	defer buf.ReleaseMulti(mb)
	if mb.IsEmpty() {
		return nil
	}

	out := make(buf.MultiBuffer, 0, len(mb))
	for _, b := range mb {
		if b == nil {
			continue
		}
		dest := w.defaultDest
		if b.UDP != nil {
			dest = *b.UDP
		}
		payloadLen := int(b.Len())
		if payloadLen > maxFramePayload {
			continue
		}

		hdrLen := 2
		if !w.isConnect {
			addrLen := uotAddrLen(dest)
			if addrLen == 0 {
				buf.ReleaseMulti(out)
				return io.ErrShortWrite
			}
			hdrLen += addrLen
		}

		pkt := buf.NewWithSize(int32(hdrLen + payloadLen))
		if !w.isConnect {
			if err := writeUoTAddr(pkt, dest); err != nil {
				pkt.Release()
				buf.ReleaseMulti(out)
				return err
			}
		}
		binary.BigEndian.PutUint16(pkt.Extend(2), uint16(payloadLen))
		if payloadLen > 0 {
			_, _ = pkt.Write(b.Bytes())
		}
		out = append(out, pkt)
	}
	if out.IsEmpty() {
		return nil
	}
	return w.writer.WriteMultiBuffer(out)
}

func (w *uotWriter) Close() error {
	return common.Close(w.writer)
}

func (w *uotWriter) Interrupt() {
	common.Interrupt(w.writer)
}
