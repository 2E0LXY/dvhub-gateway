package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"

	"strings"
	"testing"
)

func udpEth(payload []byte) []byte {
	ip := make([]byte, 20+8+len(payload))
	ip[0], ip[9] = 0x45, 17
	binary.BigEndian.PutUint16(ip[2:], uint16(len(ip)))
	binary.BigEndian.PutUint16(ip[24:], uint16(8+len(payload)))
	copy(ip[28:], payload)
	eth := make([]byte, 14)
	eth[12], eth[13] = 0x08, 0x00
	return append(eth, ip...)
}

func pcapOf(pkts ...[]byte) []byte {
	var b bytes.Buffer
	gh := make([]byte, 24)
	binary.LittleEndian.PutUint32(gh, 0xa1b2c3d4)
	binary.LittleEndian.PutUint32(gh[20:], 1)
	b.Write(gh)
	for _, p := range pkts {
		rh := make([]byte, 16)
		binary.LittleEndian.PutUint32(rh[8:], uint32(len(p)))
		binary.LittleEndian.PutUint32(rh[12:], uint32(len(p)))
		b.Write(rh)
		b.Write(p)
	}
	return b.Bytes()
}

func TestPCAPSilence(t *testing.T) {
	burst, _ := hex.DecodeString("B9E881526173002A6BB9E881526000000000000173002A6BB9E881526173002A6B")
	dmrd := append([]byte("DMRD"), make([]byte, 16)...)
	dmrd[16], dmrd[19] = 0xAB, 0xCD
	dmrd = append(dmrd, burst...)

	dsvt := make([]byte, 27)
	copy(dsvt, "DSVT")
	dsvt[4], dsvt[12], dsvt[13] = 0x20, 0x12, 0x34
	copy(dsvt[15:], []byte{0x9E, 0x8D, 0x32, 0x88, 0x26, 0x1A, 0x3F, 0x61, 0xE8})

	var out bytes.Buffer
	d := &dumper{w: bufio.NewWriter(&out), streams: map[string]*stream{}}
	if err := readPCAP(bytes.NewReader(pcapOf(udpEth(dmrd), udpEth(dsvt))), d.udp); err != nil {
		t.Fatal(err)
	}
	d.w.Flush()
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d rows:\n%s", len(lines), out.String())
	}
	for _, l := range lines[:3] {
		if !strings.HasPrefix(l, "dmr-ab0000cd,") || !strings.Contains(l, ",silence,0,0,true,124,") {
			t.Fatalf("dmr row: %s", l)
		}
	}
	if !strings.HasPrefix(lines[3], "dstar-1234,0,dstar,") || !strings.Contains(lines[3], ",0,0,true,124,") {
		t.Fatalf("dstar row: %s", lines[3])
	}
}
