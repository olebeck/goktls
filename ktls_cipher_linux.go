//go:build linux
// +build linux

package tls

import (
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

const (
	kTLS_CIPHER_AES_GCM_128              = 51
	kTLS_CIPHER_AES_GCM_128_IV_SIZE      = 8
	kTLS_CIPHER_AES_GCM_128_KEY_SIZE     = 16
	kTLS_CIPHER_AES_GCM_128_SALT_SIZE    = 4
	kTLS_CIPHER_AES_GCM_128_TAG_SIZE     = 16
	kTLS_CIPHER_AES_GCM_128_REC_SEQ_SIZE = 8

	kTLS_CIPHER_AES_GCM_256              = 52
	kTLS_CIPHER_AES_GCM_256_IV_SIZE      = 8
	kTLS_CIPHER_AES_GCM_256_KEY_SIZE     = 32
	kTLS_CIPHER_AES_GCM_256_SALT_SIZE    = 4
	kTLS_CIPHER_AES_GCM_256_TAG_SIZE     = 16
	kTLS_CIPHER_AES_GCM_256_REC_SEQ_SIZE = 8

	// AES_CCM_128 is not used as it has not been implemented in golang
	kTLS_CIPHER_AES_CCM_128              = 53
	kTLS_CIPHER_AES_CCM_128_IV_SIZE      = 8
	kTLS_CIPHER_AES_CCM_128_KEY_SIZE     = 16
	kTLS_CIPHER_AES_CCM_128_SALT_SIZE    = 4
	kTLS_CIPHER_AES_CCM_128_TAG_SIZE     = 16
	kTLS_CIPHER_AES_CCM_128_REC_SEQ_SIZE = 8

	kTLS_CIPHER_CHACHA20_POLY1305              = 54
	kTLS_CIPHER_CHACHA20_POLY1305_IV_SIZE      = 12
	kTLS_CIPHER_CHACHA20_POLY1305_KEY_SIZE     = 32
	kTLS_CIPHER_CHACHA20_POLY1305_SALT_SIZE    = 0
	kTLS_CIPHER_CHACHA20_POLY1305_TAG_SIZE     = 16
	kTLS_CIPHER_CHACHA20_POLY1305_REC_SEQ_SIZE = 8
)

type kTLSCryptoInfo struct {
	version    uint16
	cipherType uint16
}

type kTLSCryptoInfoAESGCM128 struct {
	info   kTLSCryptoInfo
	iv     [kTLS_CIPHER_AES_GCM_128_IV_SIZE]byte
	key    [kTLS_CIPHER_AES_GCM_128_KEY_SIZE]byte
	salt   [kTLS_CIPHER_AES_GCM_128_SALT_SIZE]byte
	recSeq [kTLS_CIPHER_AES_GCM_128_REC_SEQ_SIZE]byte
}

type kTLSCryptoInfoAESGCM256 struct {
	info   kTLSCryptoInfo
	iv     [kTLS_CIPHER_AES_GCM_256_IV_SIZE]byte
	key    [kTLS_CIPHER_AES_GCM_256_KEY_SIZE]byte
	salt   [kTLS_CIPHER_AES_GCM_256_SALT_SIZE]byte
	recSeq [kTLS_CIPHER_AES_GCM_256_REC_SEQ_SIZE]byte
}

// AES_CCM_128 is not used as it has not been implemented in golang
type kTLSCryptoInfoAESCCM128 struct {
	info   kTLSCryptoInfo
	iv     [kTLS_CIPHER_AES_CCM_128_IV_SIZE]byte
	key    [kTLS_CIPHER_AES_CCM_128_KEY_SIZE]byte
	salt   [kTLS_CIPHER_AES_CCM_128_SALT_SIZE]byte
	recSeq [kTLS_CIPHER_AES_CCM_128_REC_SEQ_SIZE]byte
}

type kTLSCryptoInfoCHACHA20POLY1305 struct {
	info   kTLSCryptoInfo
	iv     [kTLS_CIPHER_CHACHA20_POLY1305_IV_SIZE]byte
	key    [kTLS_CIPHER_CHACHA20_POLY1305_KEY_SIZE]byte
	salt   [kTLS_CIPHER_CHACHA20_POLY1305_SALT_SIZE]byte
	recSeq [kTLS_CIPHER_CHACHA20_POLY1305_REC_SEQ_SIZE]byte
}

const (
	kTLSCryptoInfoSize_AES_GCM_128 = 2 + 2 + kTLS_CIPHER_AES_GCM_128_IV_SIZE + kTLS_CIPHER_AES_GCM_128_KEY_SIZE +
		kTLS_CIPHER_AES_GCM_128_SALT_SIZE + kTLS_CIPHER_AES_GCM_128_REC_SEQ_SIZE

	kTLSCryptoInfoSize_AES_GCM_256 = 2 + 2 + kTLS_CIPHER_AES_GCM_256_IV_SIZE + kTLS_CIPHER_AES_GCM_256_KEY_SIZE +
		kTLS_CIPHER_AES_GCM_256_SALT_SIZE + kTLS_CIPHER_AES_GCM_256_REC_SEQ_SIZE

	kTLSCryptoInfoSize_AES_CCM_128 = 2 + 2 + kTLS_CIPHER_AES_CCM_128_IV_SIZE + kTLS_CIPHER_AES_CCM_128_KEY_SIZE +
		kTLS_CIPHER_AES_CCM_128_SALT_SIZE + kTLS_CIPHER_AES_CCM_128_REC_SEQ_SIZE

	kTLSCryptoInfoSize_CHACHA20_POLY1305 = 2 + 2 + kTLS_CIPHER_CHACHA20_POLY1305_IV_SIZE + kTLS_CIPHER_CHACHA20_POLY1305_KEY_SIZE +
		kTLS_CIPHER_CHACHA20_POLY1305_SALT_SIZE + kTLS_CIPHER_CHACHA20_POLY1305_REC_SEQ_SIZE
)

func ktlsEnableAES(
	c *Conn,
	version uint16,
	enableFunc func(c *net.TCPConn, version uint16, opt int, skip bool, key, iv, seq []byte) error,
	keyLen int,
	inKey, outKey, inIV, outIV []byte,
	clientCipher, serverCipher *any) error {
	var ulpEnabled bool

	// Try to enable Kernel TLS TX
	if !kTLSSupportTX {
		return nil
	}
	if len(outKey) == keyLen {
		if tcpConn, ok := c.conn.(*net.TCPConn); ok {
			if err := enableFunc(tcpConn, version, TLS_TX, ulpEnabled, outKey, outIV[:], c.out.seq[:]); err != nil {
				Debugln("kTLS: TLS_TX error enabling:", err)
				return err
			}
			ulpEnabled = true
			Debugln("kTLS: TLS_TX enabled")
			*clientCipher = kTLSCipher{}
			// Try to enable kTLS TX zerocopy sendfile.
			// Only enabled if the hardware supports the protocol.
			// Otherwise, get an error message which is fine.
			ktlsEnableTxZerocopySendfile(tcpConn)
		} else {
			Debugln("kTLS: TLS_TX unsupported connection type")
		}
	} else {
		Debugln("kTLS: TLS_TX unsupported key length")
	}

	// Try to enable Kernel TLS RX for TLS 1.2 or TLS 1.3 (TLS 1.3 RX is disabled on kernel < 5.19 )
	if !kTLSSupportRX || (version == VersionTLS13 && !kTLSSupportTLS13RX) {
		return nil
	}
	if len(inKey) == keyLen {
		if tcpConn, ok := c.conn.(*net.TCPConn); ok {
			if err := enableFunc(tcpConn, version, TLS_RX, ulpEnabled, inKey, inIV[:], c.in.seq[:]); err != nil {
				Debugln("kTLS: TLS_RX error enabling:", err)
				return err
			}
			Debugln("kTLS: TLS_RX enabled")
			*serverCipher = kTLSCipher{}
			// Only enable the TLS_RX_EXPECT_NO_PAD for TLS 1.3
			// TODO: safe to enable only if the remote end is trusted, otherwise
			// it is an attack vector to doubling the TLS processing cost.
			// See: https://docs.kernel.org/networking/tls.html#tls-rx-expect-no-pad
			if version == VersionTLS13 {
				ktlsEnableRxExpectNoPad(tcpConn)
			}
		} else {
			Debugln("kTLS: TLS_RX unsupported connection type")
		}
	} else {
		Debugln("kTLS: TLS_TX unsupported key length")
	}

	return nil
}

func ktlsEnableCHACHA20(c *Conn, version uint16, inKey, outKey, inIV, outIV []byte, clientCipher, serverCipher *any) error {
	var ulpEnabled bool

	// Try to enable Kernel TLS TX
	if !kTLSSupportTX {
		return nil
	}
	if tcpConn, ok := c.conn.(*net.TCPConn); ok {
		err := ktlsEnableCHACHA20POLY1305(tcpConn, version, TLS_TX, ulpEnabled, outKey, outIV, c.out.seq[:])
		if err != nil {
			Debugln("kTLS: TLS_TX error enabling:", err)
			return err
		}
		ulpEnabled = true
		Debugln("kTLS: TLS_TX enabled")
		*clientCipher = kTLSCipher{}
		// Try to enable kTLS TX zerocopy sendfile.
		// Only enabled if the hardware supports the protocol.
		// Otherwise, get an error message which is fine.
		ktlsEnableTxZerocopySendfile(tcpConn)
	} else {
		Debugln("kTLS: TLS_TX unsupported connection type")
	}

	// Try to enable Kernel TLS RX for TLS 1.2 or TLS 1.3 (TLS 1.3 RX is disabled on kernel < 5.19 )
	if !kTLSSupportRX || (version == VersionTLS13 && !kTLSSupportTLS13RX) {
		return nil
	}
	if tcpConn, ok := c.conn.(*net.TCPConn); ok {
		err := ktlsEnableCHACHA20POLY1305(tcpConn, version, TLS_RX, ulpEnabled, inKey[:], inIV[:], c.in.seq[:])
		if err != nil {
			Debugln("kTLS: TLS_RX error enabling:", err)
			return err
		}
		Debugln("kTLS: TLS_RX enabled")
		*serverCipher = kTLSCipher{}
		// Only enable the TLS_RX_EXPECT_NO_PAD for TLS 1.3
		// TODO: safe to enable only if the remote end is trusted, otherwise
		// it is an attack vector to doubling the TLS processing cost.
		// See: https://docs.kernel.org/networking/tls.html#tls-rx-expect-no-pad
		if version == VersionTLS13 {
			ktlsEnableRxExpectNoPad(tcpConn)
		}
	} else {
		Debugln("kTLS: TLS_RX unsupported connection type")
	}

	return nil
}

func ktlsEnableAES128GCM(c *net.TCPConn, version uint16, opt int, skip bool, key, iv, seq []byte) error {
	if len(key) != kTLS_CIPHER_AES_GCM_128_KEY_SIZE {
		return fmt.Errorf("kTLS: wrong key length, desired: %d, actual: %d",
			kTLS_CIPHER_AES_GCM_128_KEY_SIZE, len(key))
	}
	if version == VersionTLS12 {
		// The nounce of TLS 1.2 only has 4 bytes. So, compare with kTLS_CIPHER_AES_GCM_128_SALT_SIZE only
		if len(iv) != kTLS_CIPHER_AES_GCM_128_SALT_SIZE {
			return fmt.Errorf("kTLS: wrong iv length, desired: %d, actual: %d",
				kTLS_CIPHER_AES_GCM_128_SALT_SIZE, len(iv))
		}
		if len(seq) != kTLS_CIPHER_AES_GCM_128_REC_SEQ_SIZE {
			return fmt.Errorf("kTLS: wrong seq length, desired: %d, actual: %d",
				kTLS_CIPHER_AES_GCM_128_REC_SEQ_SIZE, len(seq))
		}
	} else {
		// The nounce of TLS 1.3 only has 12 bytes. So, compare with
		// kTLS_CIPHER_AES_GCM_128_SALT_SIZE + kTLS_CIPHER_AES_GCM_128_IV_SIZE
		if len(iv) != kTLS_CIPHER_AES_GCM_128_SALT_SIZE+kTLS_CIPHER_AES_GCM_128_IV_SIZE {
			return fmt.Errorf("kTLS: wrong iv length, desired: %d, actual: %d",
				kTLS_CIPHER_AES_GCM_128_SALT_SIZE+kTLS_CIPHER_AES_GCM_128_IV_SIZE, len(iv))
		}
		if len(seq) != kTLS_CIPHER_AES_GCM_128_REC_SEQ_SIZE {
			return fmt.Errorf("kTLS: wrong seq length, desired: %d, actual: %d",
				kTLS_CIPHER_AES_GCM_128_REC_SEQ_SIZE, len(seq))
		}
	}

	cryptoInfo := kTLSCryptoInfoAESGCM128{
		info: kTLSCryptoInfo{
			version:    version,
			cipherType: kTLS_CIPHER_AES_GCM_128,
		},
	}

	Debugf("\nkey: %x\niv: %x\nseq: %x", key, iv, seq)
	copy(cryptoInfo.key[:], key)
	copy(cryptoInfo.salt[:], iv[:kTLS_CIPHER_AES_GCM_128_SALT_SIZE])
	// TODO https://github.com/FiloSottile/go/blob/filippo%2FkTLS/src/crypto/tls/ktls.go#L73
	// the PoC of FiloSottile here is copy(cryptoInfo.iv[:], seq)
	// For TLS 1.2, its IV is 0, whereas TLS 1.3 uses the rest of 8 bytes
	copy(cryptoInfo.iv[:], iv[kTLS_CIPHER_AES_GCM_128_SALT_SIZE:])
	copy(cryptoInfo.recSeq[:], seq)

	// Assert padding isn't introduced by alignment requirements.
	if unsafe.Sizeof(cryptoInfo) != kTLSCryptoInfoSize_AES_GCM_128 {
		return fmt.Errorf("kTLS: wrong cryptoInfo size, desired: %d, actual: %d",
			kTLSCryptoInfoSize_AES_GCM_128, unsafe.Sizeof(cryptoInfo))
	}

	rwc, err := c.SyscallConn()
	if err != nil {
		return err
	}

	var err0 error
	err = rwc.Control(func(fd uintptr) {
		if !skip {
			err0 = syscall.SetsockoptString(int(fd), syscall.SOL_TCP, TCP_ULP, "tls")
			if err0 != nil {
				Debugln("kTLS: setsockopt(SOL_TCP, TCP_ULP) failed:", err0)
			}
		}
		err0 = syscall.SetsockoptString(int(fd), SOL_TLS, opt,
			string((*[kTLSCryptoInfoSize_AES_GCM_128]byte)(unsafe.Pointer(&cryptoInfo))[:]))
		if err0 != nil {
			Debugf("kTLS: setsockopt(SOL_TLS, %d) failed: %s", opt, err0)
			return
		}
	})
	if err == nil {
		err = err0
	}
	return err
}

func ktlsEnableAES256GCM(c *net.TCPConn, version uint16, opt int, skip bool, key, iv, seq []byte) error {
	if len(key) != kTLS_CIPHER_AES_GCM_256_KEY_SIZE {
		return fmt.Errorf("kTLS: wrong key length, desired: %d, actual: %d",
			kTLS_CIPHER_AES_GCM_256_KEY_SIZE, len(key))
	}
	if version == VersionTLS12 {
		// The nounce of TLS 1.2 only has 4 bytes. So, compare with kTLS_CIPHER_AES_GCM_256_SALT_SIZE only
		if len(iv) != kTLS_CIPHER_AES_GCM_256_SALT_SIZE {
			return fmt.Errorf("kTLS: wrong iv length, desired: %d, actual: %d",
				kTLS_CIPHER_AES_GCM_256_SALT_SIZE, len(iv))
		}
		if len(seq) != kTLS_CIPHER_AES_GCM_256_REC_SEQ_SIZE {
			return fmt.Errorf("kTLS: wrong seq length, desired: %d, actual: %d",
				kTLS_CIPHER_AES_GCM_256_REC_SEQ_SIZE, len(seq))
		}
	} else {
		// The nounce of TLS 1.3 only has 12 bytes. So, compare with
		// kTLS_CIPHER_AES_GCM_256_SALT_SIZE + kTLS_CIPHER_AES_GCM_256_IV_SIZE
		if len(iv) != kTLS_CIPHER_AES_GCM_256_SALT_SIZE+kTLS_CIPHER_AES_GCM_256_IV_SIZE {
			return fmt.Errorf("kTLS: wrong iv length, desired: %d, actual: %d",
				kTLS_CIPHER_AES_GCM_256_SALT_SIZE+kTLS_CIPHER_AES_GCM_256_IV_SIZE, len(iv))
		}
		if len(seq) != kTLS_CIPHER_AES_GCM_256_REC_SEQ_SIZE {
			return fmt.Errorf("kTLS: wrong seq length, desired: %d, actual: %d",
				kTLS_CIPHER_AES_GCM_256_REC_SEQ_SIZE, len(seq))
		}
	}

	cryptoInfo := kTLSCryptoInfoAESGCM256{
		info: kTLSCryptoInfo{
			version:    version,
			cipherType: kTLS_CIPHER_AES_GCM_256,
		},
	}

	Debugf("\nkey: %x\niv: %x\n seq: %x", key, iv, seq)
	copy(cryptoInfo.key[:], key)
	copy(cryptoInfo.salt[:], iv[:kTLS_CIPHER_AES_GCM_256_SALT_SIZE])
	// TODO https://github.com/FiloSottile/go/blob/filippo%2FkTLS/src/crypto/tls/ktls.go#L73
	// the PoC of FiloSottile here is copy(cryptoInfo.iv[:], seq)
	// For TLS 1.2, its IV is 0, whereas TLS 1.3 uses the rest of 8 bytes
	copy(cryptoInfo.iv[:], iv[kTLS_CIPHER_AES_GCM_256_SALT_SIZE:])
	copy(cryptoInfo.recSeq[:], seq)

	// Assert padding isn't introduced by alignment requirements.
	if unsafe.Sizeof(cryptoInfo) != kTLSCryptoInfoSize_AES_GCM_256 {
		return fmt.Errorf("kTLS: wrong cryptoInfo size, desired: %d, actual: %d",
			kTLSCryptoInfoSize_AES_GCM_256, unsafe.Sizeof(cryptoInfo))
	}

	rwc, err := c.SyscallConn()
	if err != nil {
		return err
	}

	var err0 error
	err = rwc.Control(func(fd uintptr) {
		if !skip {
			err0 = syscall.SetsockoptString(int(fd), syscall.SOL_TCP, TCP_ULP, "tls")
			if err0 != nil {
				Debugln("kTLS: setsockopt(SOL_TCP, TCP_ULP) failed:", err0)
				return
			}
		}
		err0 = syscall.SetsockoptString(int(fd), SOL_TLS, opt,
			string((*[kTLSCryptoInfoSize_AES_GCM_256]byte)(unsafe.Pointer(&cryptoInfo))[:]))
		if err0 != nil {
			Debugf("kTLS: setsockopt(SOL_TLS, %d) failed: %s", opt, err0)
			return
		}
	})
	if err == nil {
		err = err0
	}

	return err
}

func ktlsEnableCHACHA20POLY1305(c *net.TCPConn, version uint16, opt int, skip bool, key, iv, seq []byte) error {
	if len(key) != kTLS_CIPHER_CHACHA20_POLY1305_KEY_SIZE {
		return fmt.Errorf("kTLS: wrong key length, desired: %d, actual: %d",
			kTLS_CIPHER_CHACHA20_POLY1305_KEY_SIZE, len(key))
	}
	if len(iv) != kTLS_CIPHER_CHACHA20_POLY1305_IV_SIZE {
		return fmt.Errorf("kTLS: wrong iv length, desired: %d, actual: %d",
			kTLS_CIPHER_CHACHA20_POLY1305_IV_SIZE, len(iv))
	}
	if len(seq) != kTLS_CIPHER_CHACHA20_POLY1305_REC_SEQ_SIZE {
		return fmt.Errorf("kTLS: wrong seq length, desired: %d, actual: %d",
			kTLS_CIPHER_CHACHA20_POLY1305_REC_SEQ_SIZE, len(seq))
	}

	cryptoInfo := kTLSCryptoInfoCHACHA20POLY1305{
		info: kTLSCryptoInfo{
			version:    version,
			cipherType: kTLS_CIPHER_CHACHA20_POLY1305,
		},
	}

	Debugf("\nkey: %x\niv: %x\nseq: %x", key, iv, seq)
	copy(cryptoInfo.key[:], key)
	copy(cryptoInfo.iv[:], iv)
	// the salt of CHACHA20POLY1305 is 0 bytes. So, no need to copy
	copy(cryptoInfo.recSeq[:], seq)

	// Assert padding isn't introduced by alignment requirements.
	if unsafe.Sizeof(cryptoInfo) != kTLSCryptoInfoSize_CHACHA20_POLY1305 {
		return fmt.Errorf("kTLS: wrong cryptoInfo size, desired: %d, actual: %d",
			kTLSCryptoInfoSize_CHACHA20_POLY1305, unsafe.Sizeof(cryptoInfo))
	}

	rwc, err := c.SyscallConn()
	if err != nil {
		return err
	}

	var err0 error
	err = rwc.Control(func(fd uintptr) {
		if !skip {
			err0 = syscall.SetsockoptString(int(fd), syscall.SOL_TCP, TCP_ULP, "tls")
			if err0 != nil {
				Debugln("kTLS: setsockopt(SOL_TCP, TCP_ULP) failed:", err)
				return
			}
		}
		err0 = syscall.SetsockoptString(int(fd), SOL_TLS, opt,
			string((*[kTLSCryptoInfoSize_CHACHA20_POLY1305]byte)(unsafe.Pointer(&cryptoInfo))[:]))
		if err0 != nil {
			Debugf("kTLS: setsockopt(SOL_TLS, %d) failed: %s", opt, err)
			return
		}
	})
	if err == nil {
		err = err0
	}

	return err
}

func ktlsEnableTxZerocopySendfile(c *net.TCPConn) (err error) {
	if !kTLSSupportZEROCOPY {
		return nil
	}

	rwc, err := c.SyscallConn()
	if err != nil {
		return err
	}

	var err0 error
	err = rwc.Control(func(fd uintptr) {
		err0 = syscall.SetsockoptInt(int(fd), SOL_TLS, TLS_TX_ZEROCOPY_RO, 1)
		if err0 != nil {
			Debugf("kTLS: TLS_TX Zerocopy Sendfile not Enabled. Error: %s", err0)
			return
		}
		Debugln("kTLS: TLS_TX Zerocopy Sendfile Enabled")
	})
	if err == nil {
		err = err0
	}

	return
}

func ktlsEnableRxExpectNoPad(c *net.TCPConn) (err error) {
	if !kTLSSupportNOPAD {
		return nil
	}

	rwc, err := c.SyscallConn()
	if err != nil {
		return err
	}

	var err0 error
	err = rwc.Control(func(fd uintptr) {
		err0 = syscall.SetsockoptInt(int(fd), SOL_TLS, TLS_RX_EXPECT_NO_PAD, 1)
		if err0 != nil {
			Debugf("kTLS: TLS_RX Expect No Pad not Enabled. Error: %s", err0)
			return
		}
		Debugln("kTLS: TLS_RX Expect No Pad Enabled")
	})
	if err == nil {
		err = err0
	}

	return
}
