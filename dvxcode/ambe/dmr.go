package ambe

// DMR voice burst payload: 264 bits (33 bytes) as carried in MMDVM/HBP DMRD.
// AMBE frame A: bits 0-71; B: 72-107 + 156-191; C: 192-263.
// Bits 108-155 are SYNC or EMB+embedded signalling and are preserved.

func copyBits(dst []byte, di int, src []byte, si, n int) {
	for k := 0; k < n; k++ {
		setBit(dst, di+k, getBit(src, si+k, false), false)
	}
}

// ExtractDMR returns the three 72-bit AMBE frames from a burst payload.
func ExtractDMR(p [33]byte) (f [3][9]byte) {
	copyBits(f[0][:], 0, p[:], 0, 72)
	copyBits(f[1][:], 0, p[:], 72, 36)
	copyBits(f[1][:], 36, p[:], 156, 36)
	copyBits(f[2][:], 0, p[:], 192, 72)
	return
}

// InsertDMR writes three AMBE frames into a burst payload, leaving the
// sync/EMB centre untouched.
func InsertDMR(p *[33]byte, f [3][9]byte) {
	copyBits(p[:], 0, f[0][:], 0, 72)
	copyBits(p[:], 72, f[1][:], 0, 36)
	copyBits(p[:], 156, f[1][:], 36, 36)
	copyBits(p[:], 192, f[2][:], 0, 72)
}
