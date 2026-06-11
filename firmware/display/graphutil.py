# Pure helpers for the display node — no hardware imports, so they also run
# under desktop CPython and can be unit-tested (see test_graphutil.py).


def format_age(seconds):
    """Compact 'last seen' age, e.g. 45s / 12m / 3h."""
    if seconds < 0:
        seconds = 0
    if seconds < 60:
        return "%ds" % seconds
    if seconds < 3600:
        return "%dm" % (seconds // 60)
    return "%dh" % (seconds // 3600)


def value_range(points):
    """(lo, hi) spanning every bucket's min/max, expanded to >= 1 C when flat."""
    if not points:
        return (0.0, 1.0)
    lo = min(p["min"] for p in points)
    hi = max(p["max"] for p in points)
    if hi - lo < 1.0:
        mid = (hi + lo) / 2.0
        lo, hi = mid - 0.5, mid + 0.5
    return (lo, hi)


def x_for(ts, from_ts, to_ts, width):
    """Column for a timestamp on a LINEAR time axis [from_ts, to_ts]."""
    span = to_ts - from_ts
    if span <= 0:
        span = 1
    x = int((ts - from_ts) * (width - 1) / span)
    if x < 0:
        return 0
    if x > width - 1:
        return width - 1
    return x


def y_for(value, lo, hi, height):
    """Row for a value; inverted so higher temperatures are higher on screen."""
    span = hi - lo
    if span <= 0:
        span = 1
    y = int((hi - value) * (height - 1) / span)
    if y < 0:
        return 0
    if y > height - 1:
        return height - 1
    return y


def is_gap(prev_ts, ts, bucket_seconds):
    """True when buckets are missing between two points (> ~1.5 bucket apart)."""
    return (ts - prev_ts) > bucket_seconds * 1.5


def hhmm(ts, tz_offset_hours=0):
    """Format a unix timestamp as HH:MM, with an optional whole-hour TZ offset.

    Pure integer math (no time module / timezone db), so it behaves identically
    on CircuitPython and CPython.
    """
    secs = int(ts + tz_offset_hours * 3600) % 86400
    if secs < 0:
        secs += 86400
    return "%02d:%02d" % (secs // 3600, (secs % 3600) // 60)
