# Desktop CPython tests for the display's pure helpers. Run: python test_graphutil.py
import graphutil as g


def check(cond, msg):
    if not cond:
        raise SystemExit("FAIL: " + msg)


# format_age
check(g.format_age(5) == "5s", "age seconds")
check(g.format_age(120) == "2m", "age minutes")
check(g.format_age(7200) == "2h", "age hours")
check(g.format_age(-3) == "0s", "age negative clamps")

# value_range: exact span, and flat span expands to >= 1 C
lo, hi = g.value_range([{"min": 18.0, "max": 22.0}])
check(lo == 18.0 and hi == 22.0, "range exact")
lo, hi = g.value_range([{"min": 20.0, "max": 20.2}])
check(hi - lo >= 1.0, "range expands when flat")
check(g.value_range([]) == (0.0, 1.0), "range empty default")

# x_for: linear in time, clamped to [0, width-1]
check(g.x_for(0, 0, 100, 11) == 0, "x at start")
check(g.x_for(100, 0, 100, 11) == 10, "x at end")
check(g.x_for(50, 0, 100, 11) == 5, "x midpoint")
check(g.x_for(-10, 0, 100, 11) == 0, "x clamps low")
check(g.x_for(999, 0, 100, 11) == 10, "x clamps high")

# y_for: inverted (high value -> top -> y=0)
check(g.y_for(22.0, 18.0, 22.0, 11) == 0, "y high at top")
check(g.y_for(18.0, 18.0, 22.0, 11) == 10, "y low at bottom")

# is_gap: contiguous buckets are not a gap; a big step is
check(g.is_gap(0, 600, 600) is False, "contiguous bucket no gap")
check(g.is_gap(0, 2400, 600) is True, "missing buckets is a gap")

# hhmm: UTC by default, optional whole-hour offset, wraps within a day
check(g.hhmm(0) == "00:00", "hhmm epoch")
check(g.hhmm(3661) == "01:01", "hhmm h:m")
check(g.hhmm(0, 2) == "02:00", "hhmm offset")
check(g.hhmm(86400 - 60) == "23:59", "hhmm end of day")
check(g.hhmm(3600, -1) == "00:00", "hhmm negative offset")

print("ok")
