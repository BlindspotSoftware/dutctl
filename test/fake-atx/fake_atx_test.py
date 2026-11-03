#!/usr/bin/env python3
"""Checks the fake board's power logic without GPIO: python3 test/fake-atx/fake_atx_test.py"""

from types import SimpleNamespace

from fake_atx import Board

led = SimpleNamespace(value=None)
board = Board(led)
assert led.value is False
assert board.power(0.1) == "power press 0.10 s: on" and led.value is True
assert board.reset(0.1) == "reset press 0.10 s: reset" and led.value is True
assert board.power(5.5) == "power press 5.50 s: forced off" and led.value is False
assert board.power(5.5).endswith(": stays off") and led.value is False
assert board.reset(0.1).endswith(": ignored, the board is off")
assert board.power(0.1).endswith(": on") and board.power(0.1).endswith(": off") and led.value is False
print("fake_atx: ok")
