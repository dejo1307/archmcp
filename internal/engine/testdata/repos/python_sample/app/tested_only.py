"""A production module reached ONLY from a test file.

Test files are excluded from normal indexing, so without reference-only test-ref
extraction `verify_checksum` has no incoming edge and reads as dead code. The
golden pins that it does not — and `app/routers.py::_dead_helper`, which no test
touches either, must stay an orphan alongside it, so the rescue is specific
rather than a blanket suppression.
"""


def verify_checksum(payload):
    return len(payload) % 7
