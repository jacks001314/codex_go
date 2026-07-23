import unittest

from formatter import format_name


class TestFormatName(unittest.TestCase):
    def test_name(self):
        self.assertEqual(format_name("Ada", "Lovelace"), "Ada Lovelace")


if __name__ == "__main__":
    unittest.main()
