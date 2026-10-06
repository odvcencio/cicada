import gzip
import unittest
from unittest.mock import patch

from build_cc0 import canonical_gzip


class CanonicalGzipTest(unittest.TestCase):
    def test_header_is_independent_of_python_platform(self):
        payload = b"RIFF" + bytes(range(256)) * 4
        expected = bytearray(gzip.compress(payload, compresslevel=9, mtime=0))
        expected[9] = 255
        for os_byte in (0, 3, 255):
            with self.subTest(os_byte=os_byte):
                runtime_bytes = bytearray(expected)
                runtime_bytes[9] = os_byte
                with patch("build_cc0.gzip.compress", return_value=bytes(runtime_bytes)) as compress:
                    got = canonical_gzip(payload)
                    compress.assert_called_once_with(payload, compresslevel=9, mtime=0)
                self.assertEqual(got, bytes(expected))
                self.assertEqual(gzip.decompress(got), payload)

    def test_runtime_output_is_reproducible(self):
        payload = b"sample bank fixture" * 128
        first = canonical_gzip(payload)
        self.assertEqual(first[4:8], b"\0" * 4)
        self.assertEqual(first[9], 255)
        self.assertEqual(first, canonical_gzip(payload))
        self.assertEqual(gzip.decompress(first), payload)


if __name__ == "__main__":
    unittest.main()
