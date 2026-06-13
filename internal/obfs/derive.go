package obfs

import (
	"fmt"

	"github.com/pion/dtls/v3"
	"github.com/pion/srtp/v3"
)

func mapProfile(p dtls.SRTPProtectionProfile) (srtp.ProtectionProfile, error) {
	switch p {
	case dtls.SRTP_AEAD_AES_128_GCM:
		return srtp.ProtectionProfileAeadAes128Gcm, nil
	case dtls.SRTP_AES128_CM_HMAC_SHA1_80:
		return srtp.ProtectionProfileAes128CmHmacSha1_80, nil
	default:
		return 0, fmt.Errorf("unsupported srtp profile: %v", p)
	}
}

func DerivePair(conn *dtls.Conn, isClient bool) (*SRTPSender, *SRTPReceiver, error) {
	selected, ok := conn.SelectedSRTPProtectionProfile()
	if !ok {
		return nil, nil, fmt.Errorf("dtls peer did not select an srtp profile")
	}
	profile, err := mapProfile(selected)
	if err != nil {
		return nil, nil, err
	}

	state, ok := conn.ConnectionState()
	if !ok {
		return nil, nil, fmt.Errorf("dtls connection state unavailable")
	}

	cfg := srtp.Config{Profile: profile}
	if err := cfg.ExtractSessionKeysFromDTLS(&state, isClient); err != nil {
		return nil, nil, fmt.Errorf("dtls keying material export: %w", err)
	}

	sender, err := NewSRTPSender(cfg.Keys.LocalMasterKey, cfg.Keys.LocalMasterSalt, profile)
	if err != nil {
		return nil, nil, err
	}
	receiver, err := NewSRTPReceiver(cfg.Keys.RemoteMasterKey, cfg.Keys.RemoteMasterSalt, profile)
	if err != nil {
		return nil, nil, err
	}
	return sender, receiver, nil
}
