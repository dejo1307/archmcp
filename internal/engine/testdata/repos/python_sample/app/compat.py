"""Guarded module-level imports: the package-or-script dual-import idiom.

Pins v119: try/except ImportError imports register (the except fallback never
clobbers the try binding), so a call through the guarded name resolves; and a
module-level assignment whose RHS is a bare def name (`_DEFAULT_HANDLER = lookup`)
folds a value-ref, so a function installed by assignment is not read as dead.
"""

try:
    from app.db import get_user
except ImportError:
    from db import get_user

try:
    from app.db import seed as _unused_guarded
except ImportError:
    _unused_guarded = None


def lookup(user_id):
    """Call through the guarded import — must resolve to app/db.get_user."""
    return get_user(user_id)


# Install a def as a value (monkeypatch/dispatch/alias idiom): the assignment RHS
# is a use of `lookup`, so it must not read as dead.
_DEFAULT_HANDLER = lookup
