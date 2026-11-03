#!/usr/bin/env python3
"""A fake ATX mainboard on the dutctl tester for the ATX board of its PiKVM (PikvmHost in
.firmwareci/duts/dut-rpi-dutctl-tester/dut.yaml): GPIO 5 and 6 read the power and reset switch,
GPIO 13 drives the power LED input. Every press goes to stdout, so `journalctl -u fake-atx` shows
what kvmd pressed and for how long."""

import signal
import time

POWER_SWITCH, RESET_SWITCH, POWER_LED = 5, 6, 13
LONG_PRESS = 4  # seconds; an ATX board forces itself off after a power press this long


class Board:
    """The power state; led is anything with a value attribute, such as gpiozero's LED."""

    def __init__(self, led):
        self.led = led
        self.on = False
        led.value = False

    def power(self, seconds):
        if seconds >= LONG_PRESS:
            event = "forced off" if self.on else "stays off"
            self.on = False
        else:
            self.on = not self.on
            event = "on" if self.on else "off"

        self.led.value = self.on

        return f"power press {seconds:.2f} s: {event}"

    def reset(self, seconds):
        return f"reset press {seconds:.2f} s: " + ("reset" if self.on else "ignored, the board is off")


def watch(button, handle):
    """Prints handle(seconds) each time the switch behind button opens again."""
    pressed_at = 0.0

    def pressed():
        nonlocal pressed_at
        pressed_at = time.monotonic()

    button.when_pressed = pressed
    button.when_released = lambda: print(handle(time.monotonic() - pressed_at), flush=True)


def main():
    from gpiozero import LED, Button  # imported here, so fake_atx_test.py runs without gpiozero

    board = Board(LED(POWER_LED))
    buttons = Button(POWER_SWITCH, bounce_time=0.02), Button(RESET_SWITCH, bounce_time=0.02)
    watch(buttons[0], board.power)
    watch(buttons[1], board.reset)
    print("fake board ready, power off", flush=True)
    signal.pause()


if __name__ == "__main__":
    main()
