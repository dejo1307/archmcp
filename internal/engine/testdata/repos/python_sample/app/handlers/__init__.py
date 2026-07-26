"""Package re-export: `from app.handlers import process_payload` must bind to
app/handlers/impl.process_payload.

A package is a directory, so the dotted prefix "app.handlers" names no module
file — without following this re-export the caller's edge dangles as a bare
dotted string and the graph shows no connection at all.
"""

from .impl import process_payload
