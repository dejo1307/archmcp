"""Imports that do NOT run when this module is imported.

Pins v263: a function-local (lazy) import and a `if TYPE_CHECKING:` import both
carry deferred=true, so an import-closure walk can exclude them. The module-level
try/except in compat.py is the counter-case: indented, but it runs, so it carries
no such prop.
"""

from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from app.models import DataPoint


def load_store():
    """Deferred import: the circular-import workaround."""
    from app.store import Store

    return Store()


class Loader:
    def build(self):
        from app.registry import registry

        return registry
