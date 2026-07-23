import unittest

from calculator import multiply


class TestMultiply(unittest.TestCase):
    def test_two_times_three(self):
        self.assertEqual(multiply(2, 3), 6)

    def test_negative(self):
        self.assertEqual(multiply(-2, 4), -8)


if __name__ == "__main__":
    unittest.main()
