//go:build linux
// +build linux

package tls

import (
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	TCP_ULP = 31
	SOL_TLS = 282

	TLS_TX               = 1
	TLS_RX               = 2
	TLS_TX_ZEROCOPY_RO   = 3 // TX zerocopy (only sendfile now)
	TLS_RX_EXPECT_NO_PAD = 4 // Attempt opportunistic zero-copy, TLS 1.3 only

	TLS_SET_RECORD_TYPE = 1
	TLS_GET_RECORD_TYPE = 2

	kTLSOverhead = 16
)

var (
	kTLSSupport bool

	// kTLSSupportTX is true when kTLSSupport is true
	kTLSSupportTX bool
	kTLSSupportRX bool

	// kTLSSupportAESGCM128 is true when kTLSSupport is true
	kTLSSupportAESGCM128        bool
	kTLSSupportAESGCM256        bool
	kTLSSupportCHACHA20POLY1305 bool

	kTLSSupportTLS13TX bool
	// TLS1.3 RX is only supported on kernel 6+.
	// See: https://github.com/torvalds/linux/commit/ce61327ce989b63c0bd1cc7afee00e218ee696ac
	// and https://people.kernel.org/kuba/tls-1-3-rx-improvements-in-linux-5-20
	// TODO: test it on kernel 6+
	kTLSSupportTLS13RX bool

	// available in kernel >= 5.19 or 6+
	kTLSSupportZEROCOPY bool

	// available in kernel 6+
	kTLSSupportNOPAD bool
)

func init() {
	// when kernel tls module enabled, /sys/module/tls is available
	_, err := os.Stat("/sys/module/tls")
	if err != nil {
		Debugln("kTLS: kernel tls module not enabled")
		return
	}
	kTLSSupport = true && kTLSEnabled
	Debugf("kTLS Enabled Status: %v", kTLSSupport)
	// no need to check further, as KTLS is disabled
	if !kTLSSupport {
		return
	}

	var uname unix.Utsname
	if err := unix.Uname(&uname); err != nil {
		Debugf("kTLS: call uname failed %v", err)
		return
	}

	var buf [65]byte
	for i, b := range uname.Release {
		buf[i] = byte(b)
	}
	release := string(buf[:])
	if i := strings.Index(release, "\x00"); i != -1 {
		release = release[:i]
	}
	majorRelease := release[:strings.Index(release, ".")]
	minorRelease := strings.TrimLeft(release, majorRelease+".")
	minorRelease = minorRelease[:strings.Index(minorRelease, ".")]
	major, err := strconv.Atoi(majorRelease)
	if err != nil {
		Debugf("kTLS: parse major release failed %v", err)
		return
	}
	minor, err := strconv.Atoi(minorRelease)
	if err != nil {
		Debugf("kTLS: parse minor release failed %v", err)
		return
	}

	Debugf("Kernel Version: %s", release)

	if (major == 4 && minor >= 13) || major > 4 {
		kTLSSupportTX = true
		kTLSSupportAESGCM128 = true
	}

	if (major == 4 && minor >= 17) || major > 4 {
		kTLSSupportRX = true
	}

	if (major == 5 && minor >= 1) || major > 5 {
		kTLSSupportAESGCM256 = true
		kTLSSupportTLS13TX = true
	}

	if (major == 5 && minor >= 11) || major > 5 {
		kTLSSupportCHACHA20POLY1305 = true
	}

	if (major == 5 && minor >= 19) || major > 5 {
		kTLSSupportZEROCOPY = true
	}

	if major > 5 {
		kTLSSupportTLS13RX = true
		kTLSSupportNOPAD = true
	}

	Debugln("======Supported Features======")
	Debugf("kTLS TX: %v", kTLSSupportTX)
	Debugf("kTLS RX: %v", kTLSSupportRX)
	Debugf("kTLS TLS 1.3 TX: %v", kTLSSupportTLS13TX)
	Debugf("kTLS TLS 1.3 RX: %v", kTLSSupportTLS13RX)
	Debugf("kTLS TX ZeroCopy: %v", kTLSSupportZEROCOPY)
	Debugf("kTLS RX Expected No Pad: %v", kTLSSupportNOPAD)

	Debugln("=========CipherSuites=========")
	Debugf("kTLS AES-GCM-128: %v", kTLSSupportAESGCM128)
	Debugf("kTLS AES-GCM-256: %v", kTLSSupportAESGCM256)
	Debugf("kTLS CHACHA20POLY1305: %v", kTLSSupportCHACHA20POLY1305)
}

func (c *Conn) ReadFrom(r io.Reader) (n int64, err error) {
	if err := c.Handshake(); err != nil {
		return 0, err
	}
	return io.Copy(c.conn, r)
}

const maxBufferSize int64 = 4 * 1024 * 1024

func (c *Conn) writeToFile(f *os.File, remain int64) (written int64, err error, handled bool) {
	if remain <= 0 {
		return 0, nil, false
	}
	offset, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, nil, false
	}
	fi, err := f.Stat()
	if err != nil {
		return 0, nil, false
	}
	if offset+remain > fi.Size() {
		err = f.Truncate(offset + remain)
		if err != nil {
			Debugf("file truncate error: %s", err)
			return 0, nil, false
		}
	}

	// mmap must align on a page boundary
	// mmap from 0, use data from offset
	bytes, err := unix.Mmap(int(f.Fd()), 0, int(offset+remain),
		unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return 0, nil, false
	}
	defer unix.Munmap(bytes)

	bytes = bytes[offset : offset+remain]
	var (
		start = int64(0)
		end   = maxBufferSize
	)

	for {
		if end > remain {
			end = remain
		}
		//now := time.Now()
		n, err := c.Read(bytes[start:end])
		if err != nil {
			return start + int64(n), err, true
		}
		//log.Printf("read %d bytes, cost %dus", n, time.Since(now).Microseconds())
		start += int64(n)
		if start >= remain {
			break
		}

		end += int64(n)
	}
	return remain, nil, true
}

var maxSpliceSize int64 = 4 << 20

func (c *Conn) spliceToFile(f *os.File, remain int64) (written int64, err error, handled bool) {
	tcpConn, ok := c.conn.(*net.TCPConn)
	if !ok {
		return 0, nil, false
	}
	sc, err := tcpConn.SyscallConn()
	if err != nil {
		return 0, nil, false
	}
	fsc, err := f.SyscallConn()
	if err != nil {
		return 0, nil, false
	}

	var pipes [2]int
	if err := unix.Pipe(pipes[:]); err != nil {
		return 0, nil, false
	}

	prfd, pwfd := pipes[0], pipes[1]
	defer destroyTempPipe(prfd, pwfd)

	var (
		n = maxSpliceSize
		m int64
	)

	rerr := sc.Read(func(rfd uintptr) (done bool) {
		for {
			n = maxSpliceSize
			if n > remain {
				n = remain
			}
			// move tcp data to pipe
			// FIXME should not use unix.SPLICE_F_NONBLOCK, when use this flag, ktls will not advance socket buffer
			// refer: https://github.com/torvalds/linux/blob/v5.12/net/tls/tls_sw.c#L2021
			n, err = unix.Splice(int(rfd), nil, pwfd, nil, int(n), unix.SPLICE_F_MORE)
			remain -= n
			written += n
			if err == unix.EAGAIN {
				// return false to wait data from connection
				err = nil
				return false
			}

			if err != nil {
				break
			}

			// move pipe data to file
			werr := fsc.Write(func(wfd uintptr) (done bool) {
			bump:
				m, err = unix.Splice(prfd, nil, int(wfd), nil, int(n),
					unix.SPLICE_F_MOVE|unix.SPLICE_F_MORE|unix.SPLICE_F_NONBLOCK)
				if err != nil {
					return true
				}
				if m < n {
					n -= m
					goto bump
				}
				return true
			})
			if err == nil {
				err = werr
			}
			if err != nil || remain <= 0 {
				break
			}
		}
		return true
	})
	if err == nil {
		err = rerr
	}
	return written, err, true
}

// destroyTempPipe destroys a temporary pipe.
func destroyTempPipe(prfd, pwfd int) error {
	err := unix.Close(prfd)
	err1 := unix.Close(pwfd)
	if err == nil {
		return err1
	}
	return err
}

func (c *Conn) WriteTo(w io.Writer) (n int64, err error) {
	if err := c.Handshake(); err != nil {
		return 0, err
	}

	if lw, ok := w.(*LimitedWriter); ok {
		if f, ok := lw.W.(*os.File); ok {
			n, err, handled := c.spliceToFile(f, lw.N)
			if handled {
				return n, err
			}
		}
	}

	// FIXME read at least one record for io.EOF and so on ?
	//if conn, ok := w.(*net.TCPConn); ok {
	//	buf := make([]byte, 16*1024)
	//	n, err := ktlsReadRecord(conn, buf)
	//	if err != nil {
	//		wn, _ := w.Write(buf[:n])
	//		return int64(wn), err
	//	}
	//	wn, err := w.Write(buf[:n])
	//	if err != nil {
	//		return int64(wn), err
	//	}
	//}
	return io.Copy(w, c.conn)
}

func (c *Conn) IsKTLSTXEnabled() bool {
	_, ok := c.out.cipher.(kTLSCipher)
	return ok
}

func (c *Conn) IsKTLSRXEnabled() bool {
	_, ok := c.in.cipher.(kTLSCipher)
	return ok
}

func (c *Conn) enableKernelTLS(cipherSuiteID uint16, inKey, outKey, inIV, outIV []byte, clientCipher, serverCipher *any) error {
	if !kTLSSupport {
		return nil
	}
	switch cipherSuiteID {
	// Kernel TLS 1.2
	case TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, TLS_RSA_WITH_AES_128_GCM_SHA256:
		if !kTLSSupportAESGCM128 {
			return nil
		}
		Debugln("try to enable kernel tls AES_128_GCM for tls 1.2")
		return ktlsEnableAES(c, VersionTLS12, ktlsEnableAES128GCM, 16, inKey, outKey, inIV, outIV, clientCipher, serverCipher)
	case TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384, TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384, TLS_RSA_WITH_AES_256_GCM_SHA384:
		if !kTLSSupportAESGCM256 {
			return nil
		}
		Debugln("try to enable kernel tls AES_256_GCM for tls 1.2")
		return ktlsEnableAES(c, VersionTLS12, ktlsEnableAES256GCM, 32, inKey, outKey, inIV, outIV, clientCipher, serverCipher)
	case TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256, TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256:
		if !kTLSSupportCHACHA20POLY1305 {
			return nil
		}
		Debugln("try to enable kernel tls CHACHA20_POLY1305 for tls 1.2")
		return ktlsEnableCHACHA20(c, VersionTLS12, inKey, outKey, inIV, outIV, clientCipher, serverCipher)

	// Kernel TLS 1.3
	case TLS_AES_128_GCM_SHA256:
		if !kTLSSupportAESGCM128 || !kTLSSupportTLS13TX {
			return nil
		}
		Debugln("try to enable kernel tls AES_128_GCM for tls 1.3")
		return ktlsEnableAES(c, VersionTLS13, ktlsEnableAES128GCM, 16, inKey, outKey, inIV, outIV, clientCipher, serverCipher)
	case TLS_AES_256_GCM_SHA384:
		if !kTLSSupportAESGCM256 || !kTLSSupportTLS13TX {
			return nil
		}
		Debugln("try to enable kernel tls AES_256_GCM tls 1.3")
		return ktlsEnableAES(c, VersionTLS13, ktlsEnableAES256GCM, 32, inKey, outKey, inIV, outIV, clientCipher, serverCipher)
	case TLS_CHACHA20_POLY1305_SHA256:
		if !kTLSSupportCHACHA20POLY1305 || !kTLSSupportTLS13TX {
			return nil
		}
		Debugln("try to enable kernel tls CHACHA20_POLY1305 for tls 1.3")
		return ktlsEnableCHACHA20(c, VersionTLS13, inKey, outKey, inIV, outIV, clientCipher, serverCipher)
	}
	return nil
}

func ktlsReadRecord(c *net.TCPConn, b []byte) (recordType, int, error) {
	// cmsg for record type
	buffer := make([]byte, unix.CmsgSpace(1))
	cmsg := (*unix.Cmsghdr)(unsafe.Pointer(&buffer[0]))
	cmsg.SetLen(unix.CmsgLen(1))

	var iov unix.Iovec
	iov.Base = &b[0]
	iov.SetLen(len(b))

	var msg unix.Msghdr
	msg.Control = &buffer[0]
	msg.Controllen = cmsg.Len
	msg.Iov = &iov
	msg.Iovlen = 1

	rwc, err := c.SyscallConn()
	if err != nil {
		return 0, 0, err
	}

	var n int
	err0 := rwc.Read(func(fd uintptr) bool {
		flags := 0
		n, err = recvmsg(fd, &msg, flags)
		if err == unix.EAGAIN {
			// data is not ready, goroutine will be parked
			return false
		}
		// n should not be zero when err == nil
		if err == nil && n == 0 {
			err = io.EOF
		}
		return true
	})
	if err0 != nil {
		err = err0
	}
	if err != nil {
		Debugln("kTLS: recvmsg failed:", err)
		// fix bufio panic due to n == -1
		if n == -1 {
			n = 0
		}
		return 0, n, err
	}

	if n < 0 {
		return 0, 0, fmt.Errorf("unknown size received: %d", n)
	} else if n == 0 {
		return 0, 0, nil
	}

	if cmsg.Level != SOL_TLS {
		Debugf("kTLS: unsupported cmsg level: %d", cmsg.Level)
		return 0, 0, fmt.Errorf("unsupported cmsg level: %d", cmsg.Level)
	}
	if cmsg.Type != TLS_GET_RECORD_TYPE {
		Debugf("kTLS: unsupported cmsg type: %d", cmsg.Type)
		return 0, 0, fmt.Errorf("unsupported cmsg type: %d", cmsg.Type)
	}
	typ := recordType(buffer[unix.SizeofCmsghdr])
	Debugf("kTLS: recvmsg, type: %d, payload len: %d", typ, n)
	return typ, n, nil
}

func ktlsReadDataFromRecord(c *net.TCPConn, b []byte) (int, error) {
	typ, n, err := ktlsReadRecord(c, b)
	if err != nil {
		return n, err
	}
	switch typ {
	case recordTypeAlert:
		if n < 2 {
			return 0, fmt.Errorf("ktls alert payload too short")
		}
		if alert(b[1]) == alertCloseNotify {
			return 0, io.EOF
		}
		return 0, fmt.Errorf("unsupported ktls alert type: %d", b[0])
	case recordTypeApplicationData:
		return n, nil
	default:
		return 0, fmt.Errorf("unsupported ktls record type: %d", typ)
	}
}

func recvmsg(fd uintptr, msg *unix.Msghdr, flags int) (n int, err error) {
	r0, _, e1 := unix.Syscall(unix.SYS_RECVMSG, fd, uintptr(unsafe.Pointer(msg)), uintptr(flags))
	n = int(r0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func sendmsg(fd uintptr, msg *unix.Msghdr, flags int) (n int, err error) {
	r0, _, e1 := unix.Syscall(unix.SYS_SENDMSG, fd, uintptr(unsafe.Pointer(msg)), uintptr(flags))
	n = int(r0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

// Do the interface allocations only once for common
// Errno values.
var (
	errEAGAIN error = unix.EAGAIN
	errEINVAL error = unix.EINVAL
	errENOENT error = unix.ENOENT
)

// errnoErr returns common boxed Errno values, to prevent
// allocations at runtime.
func errnoErr(e unix.Errno) error {
	switch e {
	case 0:
		return nil
	case unix.EAGAIN:
		return errEAGAIN
	case unix.EINVAL:
		return errEINVAL
	case unix.ENOENT:
		return errENOENT
	}
	return e
}

func ktlsSendCtrlMessage(c *net.TCPConn, typ recordType, b []byte) (int, error) {
	// cmsg for record type
	buffer := make([]byte, unix.CmsgSpace(1))
	cmsg := (*unix.Cmsghdr)(unsafe.Pointer(&buffer[0]))
	cmsg.SetLen(unix.CmsgLen(1))
	buffer[unix.SizeofCmsghdr] = byte(typ)
	cmsg.Level = SOL_TLS
	cmsg.Type = TLS_SET_RECORD_TYPE

	var iov unix.Iovec
	iov.Base = &b[0]
	iov.SetLen(len(b))

	var msg unix.Msghdr
	msg.Control = &buffer[0]
	msg.Controllen = cmsg.Len
	msg.Iov = &iov
	msg.Iovlen = 1

	rwc, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}

	var n int
	err0 := rwc.Write(func(fd uintptr) bool {
		flags := 0
		n, err = sendmsg(fd, &msg, flags)
		if err == unix.EAGAIN {
			// data is not ready, goroutine will be parked
			return false
		}
		if err != nil {
			Debugln("kTLS: sendmsg failed:", err)
		}
		return true
	})
	if err0 != nil {
		err = err0
	}

	Debugf("kTLS: sendmsg, type: %d, payload len: %d", typ, len(b))
	return n, err
}
