package dmr

import "testing"

func FuzzBurst(f *testing.F) {
	var b Burst
	EncodeFullLC(VoiceLC(false, 1, 2), DTVoiceHeader, 1, &b)
	f.Add(b[:])
	f.Fuzz(func(t *testing.T, in []byte) {
		var b Burst
		copy(b[:], in)
		DecodeFullLC(&b, DTVoiceHeader)
		DecodeFullLC(&b, DTTerminator)
		SlotType(&b)
		_, _, _, frag, _ := EMB(&b)
		DecodeEmbedded([4][4]byte{frag, frag, frag, frag})
		_ = AMBE(&b)
		var l LC
		copy(l[:], in)
		var ta TalkerAlias
		for i := 0; i < 4; i++ {
			l[0] = byte(FLCOTAHeader + i)
			ta.Add(l)
		}
		GPS(l)
	})
}
