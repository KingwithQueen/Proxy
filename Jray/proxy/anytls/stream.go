package anytls

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/transport/pipe"
)

type stream struct {
	id   uint32
	sess *session

	pipeR *pipe.Reader
	pipeW *pipe.Writer

	ctx    context.Context
	cancel context.CancelFunc

	closeOnce    sync.Once
	finishOnce   sync.Once
	dieOnce      sync.Once
	closedByPeer atomic.Bool
	closeErr     atomic.Pointer[error]
	dieHook      func()

	reportOnce sync.Once
	bufReader  *buf.BufferedReader
}

func newStream(ctx context.Context, id uint32, sess *session) *stream {
	streamCtx, cancel := context.WithCancel(ctx)
	pipeR, pipeW := pipe.New(pipe.OptionsFromContext(ctx)...)
	return &stream{
		id:     id,
		sess:   sess,
		pipeR:  pipeR,
		pipeW:  pipeW,
		ctx:    streamCtx,
		cancel: cancel,
	}
}

func (st *stream) setError(err error) {
	if err == nil {
		return
	}
	st.closeErr.CompareAndSwap(nil, &err)
}

func (st *stream) getError() error {
	if p := st.closeErr.Load(); p != nil {
		return *p
	}
	return nil
}

func (st *stream) runDieHookOnce() {
	st.dieOnce.Do(func() {
		if st.dieHook != nil {
			st.dieHook()
		}
	})
}

// pushFromPeer delivers an incoming cmdPSH payload into the stream's pipe.
func (st *stream) pushFromPeer(mb buf.MultiBuffer) error {
	if mb.IsEmpty() {
		return nil
	}
	return st.pipeW.WriteMultiBuffer(mb)
}

// closeByPeer handles an incoming cmdFIN from the remote peer.
// Per AnyTLS spec, receiving cmdFIN closes the local stream without replying cmdFIN.
func (st *stream) closeByPeer() {
	st.closedByPeer.Store(true)
	st.closeOnce.Do(func() {
		_ = st.pipeW.Close()
		st.cancel()
	})
	st.sess.removeStream(st.id)
	st.runDieHookOnce()
}

// closeWithError terminates the stream locally (e.g., on cmdSYNACK error or session drop).
func (st *stream) closeWithError(err error, notifyPeer bool) {
	st.setError(err)
	if !notifyPeer {
		st.closedByPeer.Store(true)
	}
	st.closeOnce.Do(func() {
		st.pipeW.Interrupt()
		st.pipeR.Interrupt()
		st.cancel()
	})
	st.sess.removeStream(st.id)
	if notifyPeer {
		st.sendFINOnce()
	}
	st.runDieHookOnce()
}

// Close closes the stream cleanly from the local side and notifies the peer via cmdFIN.
func (st *stream) Close() error {
	st.closeOnce.Do(func() {
		_ = st.pipeW.Close()
		st.pipeR.Interrupt()
		st.cancel()
	})
	st.sess.removeStream(st.id)
	st.sendFINOnce()
	st.runDieHookOnce()
	return nil
}

// Interrupt aborts the stream from the local side and notifies the peer via cmdFIN.
func (st *stream) Interrupt() {
	st.closeOnce.Do(func() {
		st.pipeW.Interrupt()
		st.pipeR.Interrupt()
		st.cancel()
	})
	st.sess.removeStream(st.id)
	st.sendFINOnce()
	st.runDieHookOnce()
}

func (st *stream) sendFINOnce() {
	if st.closedByPeer.Load() || st.sess.isClosed() {
		return
	}
	st.finishOnce.Do(func() {
		if !st.closedByPeer.Load() && !st.sess.isClosed() {
			_ = st.sess.writeControlFrame(cmdFIN, st.id, nil)
		}
	})
}

func (st *stream) handshakeSuccess() {
	if st.sess.isClient {
		return
	}
	st.reportOnce.Do(func() {
		if st.sess.peerVersion.Load() >= protocolVersion {
			_ = st.sess.writeControlFrame(cmdSYNACK, st.id, nil)
		}
	})
}

func (st *stream) handshakeFailure(err error) {
	if st.sess.isClient || err == nil {
		return
	}
	st.reportOnce.Do(func() {
		if st.sess.peerVersion.Load() >= protocolVersion {
			msg := err.Error()
			if len(msg) > maxFramePayload {
				msg = msg[:maxFramePayload]
			}
			_ = st.sess.writeControlFrame(cmdSYNACK, st.id, []byte(msg))
		}
	})
}

func (st *stream) bufferedReader() *buf.BufferedReader {
	if st.bufReader == nil {
		st.bufReader = &buf.BufferedReader{Reader: st}
	}
	return st.bufReader
}

// ReadMultiBuffer implements buf.Reader.
func (st *stream) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb, err := st.pipeR.ReadMultiBuffer()
	if err != nil {
		if customErr := st.getError(); customErr != nil && errors.Cause(err) != io.EOF {
			return mb, customErr
		}
	}
	return mb, err
}

// ReadMultiBufferTimeout implements buf.TimeoutReader.
func (st *stream) ReadMultiBufferTimeout(d time.Duration) (buf.MultiBuffer, error) {
	mb, err := st.pipeR.ReadMultiBufferTimeout(d)
	if err != nil {
		if customErr := st.getError(); customErr != nil && errors.Cause(err) != io.EOF {
			return mb, customErr
		}
	}
	return mb, err
}

// WriteMultiBuffer implements buf.Writer for uplink/downlink stream payload.
func (st *stream) WriteMultiBuffer(mb buf.MultiBuffer) error {
	if mb.IsEmpty() {
		return nil
	}
	select {
	case <-st.ctx.Done():
		buf.ReleaseMulti(mb)
		if err := st.getError(); err != nil {
			return err
		}
		return io.ErrClosedPipe
	default:
	}
	return st.sess.writeStreamData(st.id, mb, st.sess.isClient)
}

var (
	_ buf.Reader           = (*stream)(nil)
	_ buf.TimeoutReader    = (*stream)(nil)
	_ buf.Writer           = (*stream)(nil)
	_ common.Closable      = (*stream)(nil)
	_ common.Interruptible = (*stream)(nil)
)
