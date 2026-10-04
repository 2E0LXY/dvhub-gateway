package dstar

import "testing"

func FuzzParse(f *testing.F) {
	h := Header{MY: "M0ABC", YOUR: "CQCQCQ"}
	f.Add(MarshalHeader(1, h))
	f.Add(MarshalVoice(1, 3, false, [9]byte{}, [3]byte{}))
	f.Add([]byte("$$CRC1234,M0ABC>API51,DSTAR*:!5340.99N/00129.86W>x"))
	f.Fuzz(func(t *testing.T, b []byte) {
		ParseDSVT(b)
		ParseHeader(b)
		ParsePosition(string(b))
		var d Demuxer
		for i := 0; i+3 <= len(b); i += 3 {
			d.Add((i/3)%21, [3]byte{b[i], b[i+1], b[i+2]})
		}
	})
}
