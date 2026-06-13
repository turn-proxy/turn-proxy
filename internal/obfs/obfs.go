package obfs

const MaxDatagram = 65535

func matchRange(b []byte, lower, upper byte) bool {
	return len(b) > 0 && b[0] >= lower && b[0] <= upper
}

func MatchDTLS(b []byte) bool { return matchRange(b, 20, 63) }

func MatchSRTP(b []byte) bool { return matchRange(b, 128, 191) }
