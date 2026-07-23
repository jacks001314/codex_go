import unittest

from service import total


class TestTotal(unittest.TestCase):
    def test_total(self):
        self.assertEqual(total([2, 3, 5]), 10)

    def test_empty(self):
        self.assertEqual(total([]), 0)


if __name__ == "__main__":
    unittest.main()
