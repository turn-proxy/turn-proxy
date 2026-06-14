package relay

import (
	"errors"
	"fmt"
	"testing"

	"github.com/pion/stun/v3"
)

func TestIsQuotaReached(t *testing.T) {
	quota := &stun.TurnError{ErrorCodeAttr: stun.ErrorCodeAttribute{Code: stun.CodeAllocQuotaReached}}
	addrFamily := &stun.TurnError{ErrorCodeAttr: stun.ErrorCodeAttribute{Code: stun.CodeAddrFamilyNotSupported}}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain", errors.New("dial tcp: connection refused"), false},
		{"address family", addrFamily, false},
		{"quota direct", quota, true},
		{"quota wrapped", fmt.Errorf("all 2 turn servers failed; last: %w", fmt.Errorf("turn allocate: %w", quota)), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsQuotaReached(tc.err); got != tc.want {
				t.Fatalf("IsQuotaReached(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
