"""Sibling module imported by bare name from analyze.py.

app/handlers/ is a package but app/handlers/tools/ is NOT (no __init__.py), so
that directory is its own source root and its modules import each other without a
package prefix. Judging importability only at the first package boundary marked
these external and the whole directory read as dead.
"""


def read_source(path):
    return path
