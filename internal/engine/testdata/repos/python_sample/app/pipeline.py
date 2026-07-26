"""Calls a method as Class.method, where the class came from an absolute import.

The walker spells the target "app.models.DataPoint.embeddable_text". Splitting it
on the last dot asks for a module named "app.models.DataPoint" and finds none, so
the class segment must move into the symbol part — otherwise the edge is dropped,
or worse, silently rewritten to app/models.embeddable_text, a different symbol.
"""

from app.models import DataPoint


def run(point):
    return DataPoint.embeddable_text(point)
