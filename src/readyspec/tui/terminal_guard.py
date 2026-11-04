"""Terminal raw-mode safety: disables IXON (Ctrl+S freeze) and ignores SIGTSTP (Section 10.2)."""

from __future__ import annotations

import signal
import sys
from collections.abc import Generator
from contextlib import contextmanager
from typing import Any


@contextmanager
def protect_terminal_flow_control() -> Generator[None, None, None]:
    """Disable POSIX software flow control (`IXON`) on stdin TTY and restore on exit."""
    saved_attrs: list[Any] | None = None
    saved_sigtstp: Any = None
    fd: int | None = None

    try:
        if hasattr(signal, "SIGTSTP"):
            saved_sigtstp = signal.getsignal(signal.SIGTSTP)
            signal.signal(signal.SIGTSTP, signal.SIG_IGN)
    except (OSError, ValueError):
        saved_sigtstp = None

    try:
        import termios

        if sys.stdin is not None and sys.stdin.isatty():
            stdin_fd = sys.stdin.fileno()
            fd = stdin_fd
            saved_attrs = termios.tcgetattr(stdin_fd)
            new_attrs = list(saved_attrs)
            new_attrs[0] = new_attrs[0] & ~(termios.IXON | termios.IXOFF)
            termios.tcsetattr(stdin_fd, termios.TCSANOW, new_attrs)
    except Exception:
        saved_attrs = None

    try:
        yield
    finally:
        if saved_attrs is not None and fd is not None:
            try:
                import termios

                termios.tcsetattr(fd, termios.TCSANOW, saved_attrs)
            except Exception:
                pass
        if saved_sigtstp is not None and hasattr(signal, "SIGTSTP"):
            try:
                signal.signal(signal.SIGTSTP, saved_sigtstp)
            except (OSError, ValueError):
                pass
