#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-or-later
"""Single-device DV30/AMBE3000 network service for DVHub Gateway."""

from __future__ import annotations

import argparse
import ipaddress
import json
import logging
import os
import select
import signal
import socket
import struct
import threading
import time
from dataclasses import dataclass
from typing import Protocol

try:
    import termios
except ImportError:  # Allows protocol/unit tests on non-POSIX development hosts.
    termios = None  # type: ignore[assignment]

START = 0x61
TYPE_CONTROL = 0x00
TYPE_AMBE = 0x01
TYPE_AUDIO = 0x02

OP_ENCODE = 0x61
OP_ENCODE_REPLY = 0x62
OP_DECODE = 0x63
OP_DECODE_REPLY = 0x64
OP_HEALTH = 0x70
OP_HEALTH_REPLY = 0x71
OP_ERROR = 0x7F

RESET_SOFT = bytes.fromhex("61 00 07 00 34 05 00 00 0F 00 00")
GET_PRODUCT = bytes.fromhex("61 00 01 00 30")
GET_VERSION = bytes.fromhex("61 00 01 00 31")
SET_DMR = bytes.fromhex("61 00 0D 00 0A 04 31 07 54 24 00 00 00 00 00 6F 48")
SET_DSTAR = bytes.fromhex("61 00 0D 00 0A 01 30 07 63 40 00 00 00 00 00 00 48")


class Vocoder(Protocol):
    product: str
    version: str

    def encode(self, pcm: bytes) -> bytes: ...
    def decode(self, ambe: bytes) -> bytes: ...


def packet(packet_type: int, payload: bytes) -> bytes:
    if len(payload) > 2048:
        raise ValueError("DVSI payload is too large")
    return bytes((START,)) + struct.pack(">H", len(payload)) + bytes((packet_type,)) + payload


class DV30Serial:
    """Minimal DVSI packet-mode driver for a DVstick 30/ThumbDV."""

    def __init__(self, device: str, baud: int = 460800, timeout: float = 0.15, mode: str = "dmr"):
        if termios is None:
            raise RuntimeError("DV30 serial access requires Linux or another POSIX host")
        self.device = device
        self.timeout = timeout
        self.mode = mode
        self.fd = os.open(device, os.O_RDWR | os.O_NOCTTY | os.O_CLOEXEC)
        self._configure(baud)
        self.lock = threading.Lock()
        self.product = "unknown"
        self.version = "unknown"
        termios.tcflush(self.fd, termios.TCIOFLUSH)
        self._initialise()

    def _configure(self, baud: int) -> None:
        speed = getattr(termios, f"B{baud}", None)
        if speed is None:
            raise ValueError(f"Unsupported serial speed: {baud}")
        attrs = termios.tcgetattr(self.fd)
        attrs[0] = 0
        attrs[1] = 0
        attrs[2] = termios.CLOCAL | termios.CREAD | termios.CS8
        attrs[3] = 0
        attrs[4] = speed
        attrs[5] = speed
        attrs[6][termios.VMIN] = 0
        attrs[6][termios.VTIME] = 0
        termios.tcsetattr(self.fd, termios.TCSANOW, attrs)

    def _write_all(self, data: bytes) -> None:
        view = memoryview(data)
        while view:
            written = os.write(self.fd, view)
            if written < 1:
                raise OSError("DV30 serial write failed")
            view = view[written:]
        termios.tcdrain(self.fd)

    def _read_exact(self, size: int, deadline: float) -> bytes:
        data = bytearray()
        while len(data) < size:
            remaining = deadline - time.monotonic()
            if remaining <= 0 or not select.select([self.fd], [], [], remaining)[0]:
                raise TimeoutError("DV30 response timeout")
            chunk = os.read(self.fd, size - len(data))
            if not chunk:
                raise OSError("DV30 serial device closed")
            data.extend(chunk)
        return bytes(data)

    def _read_packet(self, timeout: float | None = None) -> tuple[int, bytes]:
        deadline = time.monotonic() + (timeout or self.timeout)
        while self._read_exact(1, deadline)[0] != START:
            pass
        header = self._read_exact(3, deadline)
        length = struct.unpack(">H", header[:2])[0]
        if length > 2048:
            raise ValueError(f"DV30 returned invalid payload length {length}")
        return header[2], self._read_exact(length, deadline)

    def _transact(self, request: bytes, expected_type: int, timeout: float | None = None) -> bytes:
        self._write_all(request)
        response_type, payload = self._read_packet(timeout)
        if response_type != expected_type:
            raise ValueError(f"DV30 returned packet type {response_type}, expected {expected_type}")
        return payload

    def _control(self, request: bytes, field: int) -> bytes:
        payload = self._transact(request, TYPE_CONTROL, 0.75)
        if not payload or payload[0] != field:
            raise ValueError(f"Unexpected DV30 control response for field 0x{field:02x}")
        return payload[1:]

    def _reset(self) -> None:
        # DVMEGA sticks can power up in a hardware-selected mode. Clocking
        # zeroes and using RESETSOFTCFG follows the proven AMBEServer startup
        # sequence and explicitly selects packet mode.
        for _ in range(35):
            self._write_all(bytes(10))
            time.sleep(0.001)
        termios.tcflush(self.fd, termios.TCIOFLUSH)
        for _attempt in range(50):
            self._write_all(RESET_SOFT)
            for _packet_number in range(5):
                try:
                    response_type, payload = self._read_packet(0.25)
                except TimeoutError:
                    break
                if response_type == TYPE_CONTROL and payload and payload[0] == 0x39:
                    return
            time.sleep(0.01)
        raise TimeoutError("DV30 did not become ready after software reset")

    def _initialise(self) -> None:
        self._reset()
        self.product = self._control(GET_PRODUCT, 0x30).split(b"\0", 1)[0].decode("ascii", "replace")
        self.version = self._control(GET_VERSION, 0x31).split(b"\0", 1)[0].decode("ascii", "replace")
        self._control(SET_DMR if self.mode == "dmr" else SET_DSTAR, 0x0A)

    def encode(self, pcm: bytes) -> bytes:
        if len(pcm) != 320:
            raise ValueError("encode requires 320 bytes of PCM16LE")
        with self.lock:
            payload = self._transact(packet(TYPE_AUDIO, b"\x00\xA0" + pcm), TYPE_AMBE)
        if len(payload) < 11 or payload[:2] != b"\x01\x48":
            raise ValueError("DV30 returned an invalid 72-bit AMBE frame")
        return payload[2:11]

    def decode(self, ambe: bytes) -> bytes:
        if len(ambe) != 9:
            raise ValueError("decode requires one 9-byte AMBE frame")
        with self.lock:
            payload = self._transact(packet(TYPE_AMBE, b"\x01\x48" + ambe), TYPE_AUDIO)
        if len(payload) < 322 or payload[:2] != b"\x00\xA0":
            raise ValueError("DV30 returned an invalid PCM frame")
        return payload[2:322]

    def close(self) -> None:
        os.close(self.fd)


@dataclass
class Stats:
    started: float
    encoded: int = 0
    decoded: int = 0
    denied: int = 0
    errors: int = 0
    last_latency_ms: float = 0.0


class AMBEService:
    def __init__(self, vocoder: Vocoder, allowed: list[ipaddress._BaseNetwork]):
        self.vocoder = vocoder
        self.allowed = allowed
        self.stats = Stats(time.time())

    def is_allowed(self, host: str) -> bool:
        address = ipaddress.ip_address(host)
        return any(address in network for network in self.allowed)

    def handle(self, data: bytes, host: str) -> bytes | None:
        if not self.is_allowed(host):
            self.stats.denied += 1
            return None
        if not data:
            return None
        channel = data[1] if len(data) > 1 else 0
        started = time.monotonic()
        try:
            if data[0] == OP_ENCODE and len(data) == 322:
                result = bytes((OP_ENCODE_REPLY, channel)) + self.vocoder.encode(data[2:])
                self.stats.encoded += 1
            elif data[0] == OP_DECODE and len(data) == 11:
                result = bytes((OP_DECODE_REPLY, channel)) + self.vocoder.decode(data[2:])
                self.stats.decoded += 1
            elif data[0] == OP_HEALTH:
                result = bytes((OP_HEALTH_REPLY,)) + json.dumps({
                    "status": "ok", "product": self.vocoder.product, "version": self.vocoder.version,
                    "uptime_seconds": int(time.time() - self.stats.started), "encoded": self.stats.encoded,
                    "decoded": self.stats.decoded, "denied": self.stats.denied, "errors": self.stats.errors,
                    "last_latency_ms": round(self.stats.last_latency_ms, 2),
                }, separators=(",", ":")).encode()
            else:
                raise ValueError("invalid request")
            self.stats.last_latency_ms = (time.monotonic() - started) * 1000
            return result
        except Exception as exc:
            self.stats.errors += 1
            logging.warning("request from %s failed: %s", host, exc)
            return bytes((OP_ERROR, channel)) + str(exc).encode("utf-8", "replace")[:120]


def parse_endpoint(value: str) -> tuple[str, int]:
    host, separator, port_text = value.rpartition(":")
    if not separator or not host:
        raise argparse.ArgumentTypeError("endpoint must be IP:port")
    try:
        port = int(port_text)
        ipaddress.ip_address(host)
    except ValueError as exc:
        raise argparse.ArgumentTypeError(str(exc)) from exc
    if not 1 <= port <= 65535:
        raise argparse.ArgumentTypeError("port must be 1-65535")
    return host, port


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--device", required=True, help="stable /dev/serial/by-id path for the DV30")
    parser.add_argument("--baud", type=int, choices=(230400, 460800), default=460800)
    parser.add_argument("--mode", choices=("dmr", "dstar"), default="dmr")
    parser.add_argument("--bind", type=parse_endpoint, default=parse_endpoint("127.0.0.1:2460"))
    parser.add_argument("--allow", action="append", required=True, help="allowed client IPv4/IPv6 CIDR; repeat as needed")
    parser.add_argument("--verbose", action="store_true")
    args = parser.parse_args()
    allowed = [ipaddress.ip_network(value, strict=False) for value in args.allow]
    logging.basicConfig(level=logging.DEBUG if args.verbose else logging.INFO, format="%(asctime)s %(levelname)s %(message)s")

    vocoder = DV30Serial(args.device, args.baud, mode=args.mode)
    service = AMBEService(vocoder, allowed)
    sock = socket.socket(socket.AF_INET6 if ":" in args.bind[0] else socket.AF_INET, socket.SOCK_DGRAM)
    sock.bind(args.bind)
    sock.settimeout(1.0)
    running = True

    def stop(_signum: int, _frame: object) -> None:
        nonlocal running
        running = False

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    logging.info("DV30 %s %s mode=%s ready on %s:%d; allowed=%s", vocoder.product, vocoder.version, args.mode, *args.bind, args.allow)
    try:
        while running:
            try:
                data, address = sock.recvfrom(2048)
            except socket.timeout:
                continue
            response = service.handle(data, address[0])
            if response is not None:
                sock.sendto(response, address)
    finally:
        sock.close()
        vocoder.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
