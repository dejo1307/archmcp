"""A function handed to a decorator as a value.

The decorator stores the function and the framework calls it later, so it is a
real use — but it is a bare identifier, not a call, and the decorator-argument
walk only looked for nested calls.
"""

from app.tested_only import verify_checksum


def override_handler(fn):
    def wrap(target):
        return target

    return wrap


@override_handler(verify_checksum)
def handle(payload):
    return payload
