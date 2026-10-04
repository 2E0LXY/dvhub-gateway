package main

import (
	"encoding/binary"
	"errors"
	"io"
)

// Minimal classic-pcap reader yielding UDP payloads. No external deps.

type udpPacket struct {
	payload []byte
}

func readPCAP(r io.Reader, fn func(udpPacket)) error {
	var gh [24]byte
	if _, err := io.ReadFull(r, gh[:]); err != nil {
		return err
	}
	var bo binary.ByteOrder
	switch binary.LittleEndian.Uint32(gh[:4]) {
	case 0xa1b2c3d4, 0xa1b23c4d:
		bo = binary.LittleEndian
	case 0xd4c3b2a1, 0x4d3cb2a1:
		bo = binary.BigEndian
	default:
		return errors.New("not a classic pcap file (pcapng: convert with editcap -F pcap)")
	}
	link := bo.Uint32(gh[20:24])
	var rh [16]byte
	for {
		if _, err := io.ReadFull(r, rh[:]); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		n := bo.Uint32(rh[8:12])
		if n > 1<<18 {
			return errors.New("pcap: oversized record")
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return err
		}
		if p, ok := udpFrom(link, buf); ok {
			fn(udpPacket{payload: p})
		}
	}
}

func udpFrom(link uint32, b []byte) ([]byte, bool) {
	var ip []byte
	var et uint16 // ethertype: informational only; IP version is read from the header
	switch link {
	case 1: // Ethernet
		if len(b) < 14 {
			return nil, false
		}
		et, ip = binary.BigEndian.Uint16(b[12:14]), b[14:]
		if et == 0x8100 && len(b) >= 18 { // 802.1Q
			et, ip = binary.BigEndian.Uint16(b[16:18]), b[18:]
		}
	case 113: // Linux SLL
		if len(b) < 16 {
			return nil, false
		}
		et, ip = binary.BigEndian.Uint16(b[14:16]), b[16:]
	case 276: // Linux SLL2
		if len(b) < 20 {
			return nil, false
		}
		et, ip = binary.BigEndian.Uint16(b[0:2]), b[20:]
	case 0, 108: // BSD loopback
		if len(b) < 4 {
			return nil, false
		}
		ip = b[4:]
	case 101, 12: // raw IP
		ip = b
	default:
		return nil, false
	}
	if len(ip) < 1 {
		return nil, false
	}
	switch ip[0] >> 4 {
	case 4:
		ihl := int(ip[0]&0x0f) * 4
		if len(ip) < ihl+8 || ip[9] != 17 {
			return nil, false
		}
		if binary.BigEndian.Uint16(ip[6:8])&0x3fff != 0 { // fragmented
			return nil, false
		}
		u := ip[ihl:]
		l := int(binary.BigEndian.Uint16(u[4:6]))
		if l < 8 || l > len(u) {
			return nil, false
		}
		return u[8:l], true
	case 6:
		if len(ip) < 48 || ip[6] != 17 {
			return nil, false
		}
		u := ip[40:]
		l := int(binary.BigEndian.Uint16(u[4:6]))
		if l < 8 || l > len(u) {
			return nil, false
		}
		return u[8:l], true
	}
	return nil, false
}
