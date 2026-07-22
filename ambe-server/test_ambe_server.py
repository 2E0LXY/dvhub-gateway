# SPDX-License-Identifier: GPL-2.0-or-later
import ipaddress
import json
import unittest

from dvhub_ambe_server import (
    AMBEService, OP_DECODE, OP_DECODE_REPLY, OP_ENCODE, OP_ENCODE_REPLY,
    OP_HEALTH, OP_HEALTH_REPLY, packet,
)


class FakeVocoder:
    product = "AMBE3000R"
    version = "V120"

    def encode(self, pcm: bytes) -> bytes:
        if len(pcm) != 320:
            raise ValueError
        return bytes(range(9))

    def decode(self, ambe: bytes) -> bytes:
        if len(ambe) != 9:
            raise ValueError
        return b"\x34\x12" * 160


class ServerTests(unittest.TestCase):
    def setUp(self):
        self.service = AMBEService(FakeVocoder(), [ipaddress.ip_network("100.64.0.2/32")])

    def test_dvsi_packet_header(self):
        self.assertEqual(packet(2, b"abc"), b"\x61\x00\x03\x02abc")

    def test_encode(self):
        response = self.service.handle(bytes((OP_ENCODE, 7)) + bytes(320), "100.64.0.2")
        self.assertEqual(response, bytes((OP_ENCODE_REPLY, 7)) + bytes(range(9)))

    def test_decode(self):
        response = self.service.handle(bytes((OP_DECODE, 3)) + bytes(range(9)), "100.64.0.2")
        self.assertEqual(response, bytes((OP_DECODE_REPLY, 3)) + b"\x34\x12" * 160)

    def test_unapproved_source_is_silent(self):
        self.assertIsNone(self.service.handle(bytes((OP_HEALTH,)), "100.64.0.3"))
        self.assertEqual(self.service.stats.denied, 1)

    def test_health(self):
        response = self.service.handle(bytes((OP_HEALTH,)), "100.64.0.2")
        self.assertEqual(response[0], OP_HEALTH_REPLY)
        self.assertEqual(json.loads(response[1:])["product"], "AMBE3000R")


if __name__ == "__main__":
    unittest.main()
