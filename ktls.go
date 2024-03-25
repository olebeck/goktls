package tls

var kTLSEnabled bool

// kTLSCipher is a placeholder to tell the record layer to skip wrapping.
type kTLSCipher struct{}

func init() {
	kTLSEnabled = true
}
