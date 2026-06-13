package dtls

import (
	"time"

	pdtls "github.com/pion/dtls/v3"
	"github.com/pion/dtls/v3/pkg/crypto/selfsign"
)

const (
	DefaultMTU       = 1200
	HandshakeTimeout = 15 * time.Second
)

func profiles() []pdtls.SRTPProtectionProfile {
	return []pdtls.SRTPProtectionProfile{
		pdtls.SRTP_AEAD_AES_128_GCM,
		pdtls.SRTP_AES128_CM_HMAC_SHA1_80,
	}
}

func baseOptions(mtu int) ([]pdtls.Option, error) {
	cert, err := selfsign.GenerateSelfSigned()
	if err != nil {
		return nil, err
	}
	return []pdtls.Option{
		pdtls.WithCertificates(cert),
		pdtls.WithExtendedMasterSecret(pdtls.RequireExtendedMasterSecret),
		pdtls.WithSRTPProtectionProfiles(profiles()...),
		pdtls.WithMTU(mtu),
	}, nil
}

func convert[T any](opts []pdtls.Option) []T {
	out := make([]T, len(opts))
	for i, o := range opts {
		out[i] = any(o).(T)
	}
	return out
}

func serverOptions(mtu int) ([]pdtls.ServerOption, error) {
	base, err := baseOptions(mtu)
	if err != nil {
		return nil, err
	}
	return convert[pdtls.ServerOption](base), nil
}

func clientOptions(mtu int) ([]pdtls.ClientOption, error) {
	base, err := baseOptions(mtu)
	if err != nil {
		return nil, err
	}
	return append(convert[pdtls.ClientOption](base), pdtls.WithInsecureSkipVerify(true)), nil
}
