import hashlib
import os
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
SCHEMA = os.path.join(HERE, "..", "..", "spec", "bridge", "v1", "frames.schema.json")
STUB = os.path.join(HERE, "..", "synopkgland_app", "frames.pyi")


class FramesTest(unittest.TestCase):
    def test_stub_is_generated_from_the_current_schema(self):
        # scripts/python-frames.sh --check regenerates it; this needs no uv.
        with open(SCHEMA, "rb") as f:
            digest = hashlib.sha256(f.read()).hexdigest()
        with open(STUB) as f:
            header = f.read().splitlines()[1]
        self.assertEqual(header, "# schema sha256 " + digest, "run scripts/python-frames.sh")


if __name__ == "__main__":
    unittest.main()
